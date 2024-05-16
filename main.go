package main

import "proxyServer/server"

func main() {
	certMap := map[string]string{
		"filekeeper.my.id": "./certs/fullchain.pem",
	}

	keyMap := map[string]string{
		"filekeeper.my.id": "./certs/privkey.pem",
	}
	app := server.NewServer("localhost:8000", certMap, keyMap)
	app.Start()
}
