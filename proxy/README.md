# Proxy Functionalities

The proxy functionalities work in the backend. Follow the instructions below to navigate through its features and operations.

---

## Features:
1. **Node Advertisements**: Nodes advertise themselves as proxies on the DHT.
2. **HTTP/HTTPS Support**: Proxies can handle HTTP and HTTPS requests.
3. **Request Logging**: An endpoint is available to document proxy request data.

---

## Running Proxy Nodes
- Proxy nodes run on:
  - `127.0.0.1:8085`
  - `127.0.0.1:8084`

### Steps:
1. Navigate to the proxy directory:
   ```bash
   cd proxy
Start the proxy nodes:
   ```bash
   run .
```
Currently two proxy nodes have started. Both are able to make requests

Make a curl request to test http

Open another terminal and run 
  ```
curl --proxy 127.0.0.1:8085  -I http://www.example.com/
```
Or 
```
curl --proxy 127.0.0.1:8084  -I http://www.google.com/ 
```

To see the request info : 
Go to http://localhost:8085/logs
or 
Go to http://localhost:8084/logs

Click on Pretty-print.
This page shows the url, the number of bytes from client to server and the number of bytes from server to client

You need to refresh the page each time you make a request to update the log data. 


Configure your device to the proxy

Open your settings and set your IP to 127.0.0.1 

Set your port to either 8085 or 8084 

Open your browser and search up https://google.com


