# L4Proxy

L4Proxy is a rudimentary layer 4 proxy, currently supporting IPv4 TCP connections.

## Quick Start

1. Download a version from the [releases page](https://github.com/makkes/l4proxy/releases) for your platform.
1. Run the proxy, replacing `BACKEND_HOST` and `BACKEND_PORT` with your respective environment:
   ```
   l4proxy --host localhost --port 8080 --backends backend1:1234,backend2:5678
   ```
1. Test it:
   ```
   telnet localhost 8080
   ```
   you should now see you're connected to either backend1 or backend2.

## How it works

l4proxy is really mostly a proof of concept for my use case of injecting traffic into my on-premise Kubernetes cluster so don't expect miracles from it. However, I appreciate any feedback so please don't hesitate filing issues or PRs.

### Features

* Supports multiple backends
* Exercises health checks for each backend
* Randomly chooses a healthy backend for each new connection
