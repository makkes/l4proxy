# L4Proxy

L4Proxy is a rudimentary level 4 proxy, currently supporting IPv4 TCP connections.

## Quick Start

1. Download a version from the [releases page](https://github.com/makkes/l4proxy/releases) for your platform.
1. Run the proxy, replacing `BACKEND_HOST` and `BACKEND_PORT` with your respective environment:
   ```
   l4proxy -host=localhost -port=8080 -backend-host=google.com -backend-port=80
   ```
1. Test it:
   ```
   curl -i --resolve google.com:80:127.0.0.1:80 http://google.com
   ```
