package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/context"

	"github.com/coreos/etcd/client"
	"github.com/fsouza/go-dockerclient"
)

type Entry struct {
	Domains []string
	Keys    []string
	Addr    string
}

func main() {

	dkr, err := docker.NewClientFromEnv()
	if err != nil {
		log.Fatal(err)
	}

	events := make(chan *docker.APIEvents)
	err = dkr.AddEventListener(events)
	if err != nil {
		log.Fatal(err)
	}

	etc, err := client.New(client.Config{
		Endpoints:               []string{"http://" + os.Getenv("ETCD_PORT_2379_TCP_ADDR") + ":" + os.Getenv("ETCD_PORT_2379_TCP_PORT")},
		Transport:               client.DefaultTransport,
		HeaderTimeoutPerRequest: time.Second,
	})
	if err != nil {
		log.Fatal(err)
	}
	kapi := client.NewKeysAPI(etc)

	for event := range events {
		containerIDs := extractContainers(event)
		for _, containerID := range containerIDs {

			action := "set"
			ipAddr := ""
			image := ""
			name := ""
			info, err := dkr.InspectContainer(containerID)
			if err != nil && strings.HasPrefix(err.Error(), "No such container:") {
				action = "del"
				err = nil
			}
			if err != nil {
				log.Fatal(err)
			}
			if info != nil && info.NetworkSettings != nil {
				if info.NetworkSettings.IPAddress == "" {
					action = "del"
				} else {
					ipAddr = info.NetworkSettings.IPAddress
				}
			}
			if info != nil && info.Config != nil {
				name = info.Name
				image = info.Config.Image
			}

			log.Printf("%s: %s %s %s %s", action, containerID, image, name, ipAddr)

			var (
				entry     *Entry
				entryData string
			)

			resp, err := kapi.Get(context.Background(), "/swtch/"+containerID, nil)
			if err == nil {
				entryData = resp.Node.Value
				err = json.Unmarshal([]byte(resp.Node.Value), &entry)
			}
			if err != nil && !client.IsKeyNotFound(err) {
				log.Fatal(err)
			}

			if action == "del" {
				if entry == nil {
					continue
				}
				for _, key := range entry.Keys {
					_, err = kapi.Delete(context.Background(), key, nil)
					if err != nil && !client.IsKeyNotFound(err) {
						log.Fatal(err)
					}
				}
				_, err = kapi.Delete(context.Background(), "/swtch/"+containerID, nil)
				if err != nil && !client.IsKeyNotFound(err) {
					log.Fatal(err)
				}
				continue
			}

			if action == "set" {
				domains := makeAppDomains(containerID, image, name)
				e := &Entry{
					Domains: domains,
					Keys:    makeEtcdKeys(domains),
					Addr:    ipAddr,
				}

				newEntryData, err := json.Marshal(&e)
				if err != nil {
					log.Fatal(err)
				}
				if entry != nil {
					if string(newEntryData) == entryData {
						continue
					}
					for _, key := range entry.Keys {
						_, err = kapi.Delete(context.Background(), key, nil)
						if err != nil && !client.IsKeyNotFound(err) {
							log.Fatal(err)
						}
					}
					_, err = kapi.Update(context.Background(), "/swtch/"+containerID, string(newEntryData))
					if err != nil {
						log.Fatal(err)
					}
				} else {
					_, err = kapi.Create(context.Background(), "/swtch/"+containerID, string(newEntryData))
					if err != nil {
						log.Fatal(err)
					}
				}

				for _, key := range e.Keys {
					_, err = kapi.Create(context.Background(), key,
						fmt.Sprintf(`{"host":%q}`, e.Addr))
					if err != nil {
						log.Fatal(err)
					}
				}

			}
		}
	}
}

func extractContainers(event *docker.APIEvents) []string {
	c := make([]string, 0, 4)
	if event.Type == "container" {
		c = append(c, event.ID)
	}
	if event.Actor.Attributes != nil {
		if event.Actor.Attributes["container"] != "" {
			c = append(c, event.Actor.Attributes["container"])
		}
	}

	sort.Strings(c)
	out := c[:0]
	last := ""
	for _, id := range c {
		if id != last {
			last = id
			out = append(out, id)
		}
	}
	return out
}

func makeAppDomains(containerID, image, name string) []string {

	if idx := strings.LastIndexByte(image, '/'); idx >= 0 {
		image = image[idx+1:]
	}
	if idx := strings.IndexByte(image, ':'); idx >= 0 {
		image = image[:idx]
	}
	if idx := strings.LastIndexByte(name, '/'); idx >= 0 {
		name = name[idx+1:]
	}

	var names []string
	names = append(names, containerID[:12]+".switch")
	names = append(names, name+"."+image+".switch")
	names = append(names, containerID[:12]+"."+image+".switch")
	sort.Strings(names)
	return names
}

func makeEtcdKeys(domains []string) []string {
	var keys []string
	for _, name := range domains {
		parts := strings.Split(name, ".")
		for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
			parts[i], parts[j] = parts[j], parts[i]
		}
		keys = append(keys, "/skydns/"+strings.Join(parts, "/"))
	}
	sort.Strings(keys)
	return keys
}
