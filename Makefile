
switch-image:
	GOOS=linux GOARCH=amd64 go build -o switch .
	docker build -t mrhenry/switch:b1 .
