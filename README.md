# Tunneler

<h1 align="center">
  <br>
  Tunneler
  <br>
</h1>
<h4 align="center">HTTP(s) over reverse SSH tunnel</h4>
<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache%202.0-blue?style=flat-square" alt="License: Apache License 2.0"></a>
</p>
<br>

## Usage

### Run server

```sh
go run -v ./cmd/tunneler \
  -tls-cert-path ~/certs/127.0.0.1.pem \
  -tls-key-path ~/certs/127.0.0.1-key.pem \
  -host-key-path ~/.ssh/ssh_host_key
```

### Create a tunnel

```sh
curl -v -X PUT 127.0.0.1:8080/tunnels -d '{"LocalAddr": "localhost:4545"}'
```

### Sample response

```json
{
    "TunnelCommand": "ssh -N -T -Riui8tje43pb3zprqgnt436aqpw:8080:localhost:4545 streamer45-ubuntu -p 2222",
    "URLs": [
        "http://127.0.0.1:8080/tunnels/iui8tje43pb3zprqgnt436aqpw/",
        "https://127.0.0.1:8443/tunnels/iui8tje43pb3zprqgnt436aqpw/"
    ]
}
```

After the local side runs `TunnelCommand`, it should be possible to access to the local resource (i.e. `LocalAddr`) through the provided URLs.
