module github.com/freetetra/server

go 1.24.0

toolchain go1.24.1

require (
	git.cheetah.cat/tetrapack/go-zello-client v1.8.4
	github.com/eclipse/paho.mqtt.golang v1.5.1
	github.com/google/uuid v1.6.0
	github.com/gorilla/websocket v1.5.3
)

require (
	golang.org/x/net v0.44.0 // indirect
	golang.org/x/sync v0.17.0 // indirect
)

replace git.cheetah.cat/tetrapack/go-zello-client => ./_other_repos_/go-zello-client
