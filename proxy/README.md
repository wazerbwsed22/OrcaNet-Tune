Proxy : The proxies functionalities work in the backend only. The following instructions are given to navigate through this. 

Features
Nodes advertising themselves as proxies on the dht 
Proxies able to make http/https requests
Uses an endpoint to document proxy request data 

:

Running proxy node : 127.0.0.1:8085 and 127.0.0.1:8084

cd to the proxy
Run go run . on the terminal
Currently two proxy nodes have started. Both are able to make requests

Make a curl request to test http
Open another terminal
Run curl --proxy 127.0.0.1:8085  -I http://www.example.com/ 
Run curl --proxy 127.0.0.1:8084  -I http://www.google.com/ 


Make a curl request to test https
Open another terminal
Run curl --proxy 127.0.0.1:8085  -I https://www.example.com/ 
Run curl --proxy 127.0.0.1:8084  -I https://www.google.com/ 


To see the request info : 
Go to http://localhost:8085/logs
or 
Go to http://localhost:8084/logs
Click on Pretty-print
It shows the url, the number of bytes from client to server and the number of bytes from server to client

Congiure your device to the proxy
Open your settings and set your IP to 127.0.0.1 
Set your port to either 8085 or 8084 
Open your browser and search up https://google.com




