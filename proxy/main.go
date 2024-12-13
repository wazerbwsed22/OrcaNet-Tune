package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	record "github.com/libp2p/go-libp2p-record"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
	"github.com/multiformats/go-multiaddr"
	"github.com/multiformats/go-multihash"
)

// ProxyClient struct to store proxy node info
type ProxyClient struct {
	Name       string  `json:"name"`
	IP         string  `json:"ip"`
	Port       string  `json:"port"`
	PricePerMB int     `json:"price_per_mb"`
	IsMine     bool    `json:"is_mine"`
	PeerId     peer.ID `json:"peer_id"`
}

// Constants for HTTP Proxy
const (
	ORCA_NET_CLIENT_ID_HEADER = "orca-net-client-id"
	ORCA_NET_AUTH_KEY_HEADER  = "orca-net-token"
)

var (
	relay_node_addr = "/ip4/130.245.173.221/tcp/4001/p2p/12D3KooWDpJ7As7BWAwRMfu1VU2WCqNjvq387JEYKDBj4kx6nXTN"
	// bootstrap_node_addr = "/ip4/127.0.0.1/tcp/49574/12D3KooWH17Pwmfmqgf8UwRDEbg8xxC9AKowXrWicGnK9uyAAirr"
	bootstrap_node_addr = "/ip4/130.245.173.222/tcp/61020/p2p/12D3KooWM8uovScE5NPihSCKhXe8sbgdJAi88i2aXT2MmwjGWoSX"
	globalCtx           = context.Background() // Initialized here
	node_id             = "114644332"          // give your SBU ID
	// bootstrap_seed      = "random_string"
)

// ProxyClientsTable manages client authentication
type ProxyClientsTable struct {
	clients []ProxyClient
}

func NewProxyClientsTable() *ProxyClientsTable {
	return &ProxyClientsTable{clients: []ProxyClient{}}

}

func (p *ProxyClientsTable) AddProxyClient(name, ip, port string, isMine bool, peerid peer.ID) {
	p.clients = append(p.clients, ProxyClient{Name: name, IP: ip, Port: port, IsMine: isMine, PeerId: peerid})
}

func (p *ProxyClientsTable) ListProxies() {
	fmt.Println("Available proxies:")
	for i, client := range p.clients {
		fmt.Printf("%d: %s (IP: %s, Port: %s)\n", i+1, client.Name, client.IP, client.Port)
	}
}

func (p *ProxyClientsTable) GetProxyClient(proxyID string) *ProxyClient {
	// Iterate through the list of proxy clients to find the matching proxyID
	for _, client := range p.clients {
		if client.Name == proxyID {
			return &client // Return the pointer to the matched ProxyClient
		}
	}
	// Return nil if no matching proxy client is found
	return nil
}

type CustomValidator struct{}

// Validate checks the validity of the record.
func (cv *CustomValidator) Validate(key string, value []byte) error {
	// Add your custom validation logic here.
	// For example, ensure the value is not empty:
	if len(value) == 0 {
		return fmt.Errorf("value for key %s is empty", key)
	}
	return nil
}

// Select chooses between conflicting records.
func (cv *CustomValidator) Select(key string, vals [][]byte) (int, error) {
	// Add logic to select the best record if there are conflicts.
	// For simplicity, select the first record.
	return 0, nil
}

func connectToPeer(node host.Host, peerAddr string) {
	addr, err := multiaddr.NewMultiaddr(peerAddr)
	if err != nil {
		log.Printf("Failed to parse peer address: %s", err)
		return
	}

	info, err := peer.AddrInfoFromP2pAddr(addr)
	if err != nil {
		log.Printf("Failed to get AddrInfo from address: %s", err)
		return
	}

	node.Peerstore().AddAddrs(info.ID, info.Addrs, peerstore.PermanentAddrTTL)
	err = node.Connect(globalCtx, *info) // Ensure globalCtx is passed
	if err != nil {
		log.Printf("Failed to connect to peer: %s", err)
		return
	}

	fmt.Println("Connected to:", info.ID)
}

func provideKey(ctx context.Context, dht *dht.IpfsDHT, key string) error {
	data := []byte(key)
	hash := sha256.Sum256(data)
	mh, err := multihash.EncodeName(hash[:], "sha2-256")
	if err != nil {
		return fmt.Errorf("error encoding multihash: %v", err)
	}
	c := cid.NewCidV1(cid.Raw, mh)

	// Start providing the key
	err = dht.Provide(ctx, c, true)
	if err != nil {
		return fmt.Errorf("failed to start providing key: %v", err)
	}
	return nil
}

func hashFile(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("failed to hash file: %w", err)
	}

	hash := hasher.Sum(nil)
	return fmt.Sprintf("%x", hash), nil
}

func makeReservation(node host.Host) {
	ctx := globalCtx
	relayInfo, err := peer.AddrInfoFromString(relay_node_addr)
	if err != nil {
		log.Fatalf("Failed to create addrInfo from string representation of relay multiaddr: %v", err)
	}
	_, err = client.Reserve(ctx, node, *relayInfo)
	if err != nil {
		log.Fatalf("Failed to make reservation on relay: %v", err)
	}
	fmt.Printf("Reservation successfull \n")
}

func handlePeerExchange(node host.Host) {
	relayInfo, _ := peer.AddrInfoFromString(relay_node_addr)
	node.SetStreamHandler("/orcanet/p2p", func(s network.Stream) {
		defer s.Close()

		buf := bufio.NewReader(s)
		peerAddr, err := buf.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				fmt.Printf("error reading from stream: %v", err)
			}
		}
		peerAddr = strings.TrimSpace(peerAddr)
		var data map[string]interface{}
		err = json.Unmarshal([]byte(peerAddr), &data)
		if err != nil {
			fmt.Printf("error unmarshaling JSON: %v", err)
		}
		if knownPeers, ok := data["known_peers"].([]interface{}); ok {
			for _, peer := range knownPeers {
				fmt.Println("Peer:")
				if peerMap, ok := peer.(map[string]interface{}); ok {
					if peerID, ok := peerMap["peer_id"].(string); ok {
						if string(peerID) != string(relayInfo.ID) {
							connectToPeerUsingRelay(node, peerID)
						}
					}
				}
			}
		}
	})
}

// Method to get all proxies
func (pct *ProxyClientsTable) GetAllProxies() []ProxyClient {
	return pct.clients
}

func connectToPeerUsingRelay(node host.Host, targetPeerID string) {
	ctx := globalCtx
	targetPeerID = strings.TrimSpace(targetPeerID)
	relayAddr, err := multiaddr.NewMultiaddr(relay_node_addr)
	if err != nil {
		log.Printf("Failed to create relay multiaddr: %v", err)
	}
	peerMultiaddr := relayAddr.Encapsulate(multiaddr.StringCast("/p2p-circuit/p2p/" + targetPeerID))

	relayedAddrInfo, err := peer.AddrInfoFromP2pAddr(peerMultiaddr)
	if err != nil {
		log.Println("Failed to get relayed AddrInfo: %w", err)
		return
	}

	err = node.Connect(ctx, *relayedAddrInfo) // Connect to the peer through the relay
	if err != nil {
		log.Println("Failed to connect to peer through relay: %w", err)
		return
	}

	fmt.Printf("Connected to peer via relay: %s\n", targetPeerID)
}

func handleInput(ctx context.Context, dht *dht.IpfsDHT, node host.Host, proxyClientsTable *ProxyClientsTable) {
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("User Input \n ")
	for {
		fmt.Print("> ")
		input, _ := reader.ReadString('\n') // Read input from keyboard
		input = strings.TrimSpace(input)    // Trim any trailing newline or spaces
		args := strings.Split(input, " ")
		if len(args) < 1 {
			fmt.Println("No command provided")
			continue
		}
		command := args[0]
		command = strings.ToUpper(command)
		switch command {
		case "GET":
			if len(args) < 2 {
				fmt.Println("Expected key")
				continue
			}
			key := args[1]
			dhtKey := "/orcanet/" + key
			res, err := dht.GetValue(ctx, dhtKey)
			if err != nil {
				fmt.Printf("Failed to get record: %v\n", err)
				continue
			}
			fmt.Printf("Response: %s\n", string(res))

			if err != nil {
				panic(err)
			}
			fmt.Println("Peer 1 ID:", node.ID())
			fmt.Println("Peer 1 Listening on:", node.Addrs())
			fmt.Println("File received successfully.")
		case "GET_PROVIDERS":
			if len(args) < 2 {
				fmt.Println("Expected key")
				continue
			}
			key := args[1]
			data := []byte(key)
			hash := sha256.Sum256(data)
			mh, err := multihash.EncodeName(hash[:], "sha2-256")
			if err != nil {
				fmt.Printf("Error encoding multihash: %v\n", err)
				continue
			}
			c := cid.NewCidV1(cid.Raw, mh)
			providers := dht.FindProvidersAsync(ctx, c, 20)

			fmt.Println("Searching for providers...")
			for p := range providers {
				if p.ID == peer.ID("") {
					break
				}
				fmt.Printf("Found provider: %s\n", p.ID.String())
				for _, addr := range p.Addrs {
					fmt.Printf(" - Address: %s\n", addr.String())
				}
			}
		case "ADVERTISE_ME":
			myProxy := proxyClientsTable.GetProxyClient("mine")
			if myProxy == nil {
				fmt.Println("No proxy with the name 'mine' found")
				continue
			}

			fmt.Printf("Starting proxy '%s' with Peer ID %s on %s:%s...\n", myProxy.Name, myProxy.PeerId.String(), myProxy.IP, myProxy.Port)
			dhtKey := "/orcanet/proxies/" + myProxy.IP

			// Check if the key already exists in the DHT
			existingValue, err := dht.GetValue(ctx, dhtKey)
			if err == nil && existingValue != nil {
				// If the key exists, overwrite the existing value
				fmt.Printf("Proxy %s is already in the DHT with key: %s. Overwriting...\n", myProxy.Name, dhtKey)
			} else if err != nil && !strings.Contains(err.Error(), "routing: not found") {
				log.Printf("Error checking DHT for key %s: %s\n", dhtKey, err)
				continue
			}

			// Prepare the provider record
			providerRecord := map[string]interface{}{
				"peerID": node.ID().String(),
				"ip":     myProxy.IP,
				"port":   myProxy.Port,
			}
			peerID := node.ID().String()

			// Print the peer ID
			fmt.Printf("Your peer ID is: %s\n", peerID)

			providerRecordJSON, err := json.Marshal(providerRecord)
			fmt.Printf("Your PROVIDER RECORD %s\n", providerRecordJSON)

			if err != nil {
				fmt.Printf("Failed to serialize provider record for 'mine': %v\n", err)
				continue
			}

			// Put (overwrite) the record into the DHT
			err = dht.PutValue(ctx, dhtKey, providerRecordJSON)

			if err != nil {
				fmt.Printf("Failed to advertise proxy '%s' in the DHT: %v\n", dhtKey, err)
				continue
			}

			// Confirm the update
			updatedValue, err := dht.GetValue(ctx, dhtKey)
			if err != nil {
				fmt.Printf("Error retrieving value for key %s after putting it in DHT: %v\n", dhtKey, err)
				continue
			}

			if len(updatedValue) == 0 {
				fmt.Printf("The DHT key %s was overwritten with an empty value.\n", dhtKey)
			} else {
				fmt.Printf("Successfully advertised your proxy 'mine' in the DHT with key: %s\n", dhtKey)

				log.Fatal(http.ListenAndServe(
					":8082",
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.Method == http.MethodConnect {
							handleTunnel(w, r)
						} else {
							if r.URL.Path == "/logs" {
								getLoggedRequests(w, r)
							} else {
								handleHTTP(w, r)
							}

						}
					})),
				)
			}

		case "REMOVE_PROXY":
			// Retrieve the "mine" proxy from the proxy clients table
			myProxy := proxyClientsTable.GetProxyClient("mine")
			if myProxy == nil {
				fmt.Println("No proxy with the name 'mine' found")
				continue
			}

			// Construct the DHT key for "mine" proxy using IP and Port
			dhtKey := "/orcanet/proxies/" + myProxy.IP + ":" + myProxy.Port

			// Overwrite the key in the DHT with an empty value to simulate removal
			err := dht.PutValue(ctx, dhtKey, []byte{})
			if err != nil {
				fmt.Printf("Failed to remove proxy '%s' from the DHT: %v\n", dhtKey, err)
				continue
			}

			fmt.Printf("Successfully removed proxy 'mine' from the DHT with key: %s\n", dhtKey)

		case "ADVERTISE_PROXY":
			for _, proxy := range proxyClientsTable.GetAllProxies() {
				// Skip your own proxy
				if proxy.IsMine {
					continue
				}

				// Construct a unique DHT key for each proxy
				dhtKey := "/orcanet/proxies/" + proxy.IP + proxy.Port
				// Check if the key already exists in the DHT
				existingValue, err := dht.GetValue(ctx, dhtKey)
				if err == nil && existingValue != nil {
					fmt.Printf("Proxy %s is already in the DHT with key: %s\n", proxy.Name, dhtKey)
					continue
				} else if err != nil && !strings.Contains(err.Error(), "routing: not found") {
					// If the error is something other than "not found", log it and skip
					log.Printf("Error checking DHT for key %s: %s\n", dhtKey, err)
					continue
				}
				// Construct the proxy record
				proxyRecord := map[string]interface{}{
					"name": proxy.Name,
					"ip":   proxy.IP,
					"port": proxy.Port,
				}
				// Serialize the proxy record as JSON
				proxyRecordJSON, err := json.Marshal(proxyRecord)
				if err != nil {
					log.Printf("Failed to serialize proxy record for %s: %s", proxy.Name, err)
					continue
				}

				// Store the proxy record in the DHT
				err = dht.PutValue(ctx, dhtKey, proxyRecordJSON)
				if err != nil {
					log.Printf("Failed to put proxy record for %s: %s", proxy.Name, err)
					continue
				}

				fmt.Printf("Proxy %s advertised successfully with key: %s\n", proxy.Name, dhtKey)
			}

		case "PUT":
			if len(args) < 2 {
				fmt.Println("Expected file path")
				continue
			}
			filePath := args[1]

			// Calculate the hash of the file
			hash, err := hashFile(filePath)
			if err != nil {
				fmt.Printf("Error calculating file hash: %v\n", err)
				continue
			}

			dhtKey := "/orcanet/" + hash
			providerID := node.ID().String() // Use the node ID as the provider record
			addrs := node.Addrs()            // Get the node's Multiaddresses

			// Convert multiaddresses to string format
			var addrStrings []string
			for _, addr := range addrs {
				addrStrings = append(addrStrings, addr.String())
			}

			// Combine Peer ID and Multiaddresses as the value
			providerRecord := map[string]interface{}{
				"peerID": providerID,
				"addrs":  addrStrings,
			}

			// Encode the provider record as JSON (or any other serialization format)
			providerRecordJSON, err := json.Marshal(providerRecord)
			if err != nil {
				log.Printf("Failed to serialize provider record: %s", err)
				continue
			}

			// Put the file hash (key) with the provider record (value) in the DHT
			err = dht.PutValue(ctx, dhtKey, providerRecordJSON)
			if err != nil {
				log.Printf("Failed to put record: %s", err)
				continue
			}

			log.Println(dhtKey)

			err = provideKey(ctx, dht, hash)
			if err != nil {
				fmt.Printf("Failed to provide key: %v\n", err)
				continue
			}

			fmt.Printf("Record stored successfully with key: %s and value : %s\n", dhtKey, providerID)
		case "PUT_PROVIDER":
			if len(args) < 2 {
				fmt.Println("Expected key")
				continue
			}
			key := args[1]
			provideKey(ctx, dht, key)
		default:
			fmt.Println("Expected GET, GET_PROVIDERS, PUT or PUT_PROVIDER")
		}
	}
}

// Set up the internal HTTP server to handle incoming requests

func generatePrivateKeyFromSeed(seed []byte) (crypto.PrivKey, error) {
	hash := sha256.Sum256(seed) // Generate deterministic key material
	// Create an Ed25519 private key from the hash
	privKey, _, err := crypto.GenerateEd25519Key(
		bytes.NewReader(hash[:]),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to generate private key: %w", err)
	}
	return privKey, nil
}

// Function to create libp2p node with proxy capability
func createNode(proxyClientsTable *ProxyClientsTable) (host.Host, *dht.IpfsDHT, error) {
	ctx := context.Background()
	seed := []byte(node_id)
	customAddr, err := multiaddr.NewMultiaddr("/ip4/0.0.0.0/tcp/0")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse multiaddr: %w", err)
	}
	privKey, err := generatePrivateKeyFromSeed(seed)
	if err != nil {
		log.Fatal(err)
	}
	relayAddr, err := multiaddr.NewMultiaddr(relay_node_addr)
	if err != nil {
		log.Fatalf("Failed to create relay multiaddr: %v", err)
	}

	// Convert the relay multiaddress to AddrInfo
	relayInfo, err := peer.AddrInfoFromP2pAddr(relayAddr)
	if err != nil {
		log.Fatalf("Failed to create AddrInfo from relay multiaddr: %v", err)
	}

	node, err := libp2p.New(
		libp2p.ListenAddrs(customAddr),
		libp2p.Identity(privKey),
		libp2p.NATPortMap(),
		libp2p.EnableNATService(),
		libp2p.EnableAutoRelayWithStaticRelays([]peer.AddrInfo{*relayInfo}),
		libp2p.EnableRelayService(),
		libp2p.EnableHolePunching(),
	)

	if err != nil {
		return nil, nil, err
	}

	dhtRouting, err := dht.New(ctx, node, dht.Mode(dht.ModeClient))
	if err != nil {
		return nil, nil, err
	}
	namespacedValidator := record.NamespacedValidator{
		"orcanet": &CustomValidator{}, // Add a custom validator for the "orcanet" namespace
	}

	dhtRouting.Validator = namespacedValidator // Configure the DHT to use the custom validator

	err = dhtRouting.Bootstrap(ctx)
	if err != nil {
		return nil, nil, err
	}
	fmt.Println("DHT bootstrap complete.")
	// Set up notifications for new connections
	// node.Network().Notify(&network.NotifyBundle{
	// 	ConnectedF: func(n network.Network, conn network.Conn) {
	// 		fmt.Printf("Notification: New peer connected %s\n", conn.RemotePeer().String())
	// 	},
	// })

	fmt.Println("Node multiaddresses:", node.Addrs())
	fmt.Println("Node Peer ID:", node.ID())

	// If the node is set to act as a proxy, start the proxy server and advertise it
	// if isProxy {
	fmt.Println("Starting HTTP Proxy...")
	// go func() {
	// 	StartHTTPProxy(proxyClientsTable)
	// 	// if err := StartHTTPProxy(proxyClientsTable); err != nil {
	// 	// 	log.Fatalf("Failed to start HTTP Proxy: %v", err)
	// 	// }
	// }()
	return node, dhtRouting, nil
}

func createNode2(proxyClientsTable *ProxyClientsTable, node_id string) (host.Host, error) {
	seed := []byte(node_id)
	customAddr, err := multiaddr.NewMultiaddr("/ip4/0.0.0.0/tcp/0")
	if err != nil {
		return nil, fmt.Errorf("failed to parse multiaddr: %w", err)
	}

	privKey, err := generatePrivateKeyFromSeed(seed)
	if err != nil {
		log.Fatal(err)
	}

	relayAddr, err := multiaddr.NewMultiaddr(relay_node_addr)
	if err != nil {
		log.Fatalf("Failed to create relay multiaddr: %v", err)
	}

	// Convert the relay multiaddress to AddrInfo
	relayInfo, err := peer.AddrInfoFromP2pAddr(relayAddr)
	if err != nil {
		log.Fatalf("Failed to create AddrInfo from relay multiaddr: %v", err)
	}

	node, err := libp2p.New(
		libp2p.ListenAddrs(customAddr),
		libp2p.Identity(privKey),
		libp2p.NATPortMap(),
		libp2p.EnableNATService(),
		libp2p.EnableAutoRelayWithStaticRelays([]peer.AddrInfo{*relayInfo}),
		libp2p.EnableRelayService(),
		libp2p.EnableHolePunching(),
	)

	if err != nil {
		return nil, err
	}

	fmt.Println("Node multiaddresses:", node.Addrs())
	fmt.Println("Node Peer ID:", node.ID())

	return node, nil
}

// Function to get proxy details from the DHT
func GetProxy(ctx context.Context, dht *dht.IpfsDHT, proxyName string) (*ProxyClient, error) {
	// Construct the key for the proxy
	key := "/proxy-providers/" + proxyName

	// Get the value associated with the key from the DHT
	value, err := dht.GetValue(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get proxy: %w", err)
	}

	// Deserialize the JSON into a ProxyClient object
	var proxy ProxyClient
	err = json.Unmarshal(value, &proxy)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal proxy data: %w", err)
	}

	// Return the ProxyClient object
	return &proxy, nil
}

// Main libp2p and DHT setup functions...
// Including other parts from your existing code

func chooseProxy(proxyClientsTable *ProxyClientsTable) {
	print("in choose proxy")
	proxyClientsTable.ListProxies()

	fmt.Print("Choose a proxy by entering its number: ")
	var choice int
	fmt.Scan(&choice)

	if choice < 1 || choice > len(proxyClientsTable.clients) {
		fmt.Println("Invalid choice!")
		return
	}

	selectedProxy := proxyClientsTable.clients[choice-1]
	fmt.Printf("You selected proxy: %s (IP: %s, Port: %s)\n", selectedProxy.Name, selectedProxy.IP, selectedProxy.Port)

	// Connect to the selected proxy
	connectToProxy(selectedProxy)

}

// Function to create a new peer ID
func createNewPeerID() (peer.ID, error) {
	// Generate a new private key (this will give a unique peer ID each time)
	priv, _, err := crypto.GenerateKeyPair(crypto.Ed25519, 256)
	if err != nil {
		return "", fmt.Errorf("failed to generate key pair: %v", err)
	}

	// Generate the peer ID from the private key
	peerID, err := peer.IDFromPublicKey(priv.GetPublic())
	if err != nil {
		return "", fmt.Errorf("failed to generate peer ID: %v", err)
	}

	return peerID, nil
}

func connectToProxy(proxy ProxyClient) {
	fmt.Println("Comes to connect proxy function and starting proxy")

	// Channel to signal server readiness
	serverReady := make(chan struct{})

	// Start the selected proxy server in a goroutine
	go func() {
		// HTTP handler logic for the proxy server
		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			fmt.Printf("Received request at proxy %s: %s %s\n", proxy.Name, r.Method, r.URL.String())
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Proxy server is running and handling requests!"))
		})

		// Notify that the server is ready after the listener starts
		go func() {
			serverReady <- struct{}{}
		}()

		// Start listening on the selected proxy's IP and Port
		log.Println("Starting HTTP server on :8020")
		err := http.ListenAndServe("8020", nil)
		if err != nil {
			fmt.Printf("Failed to start proxy server %s at %s: %v\n", proxy.Name, err)
		}
	}()

	// Wait for server readiness
	<-serverReady
	fmt.Println("Proxy server is ready and running...")

	// Simulate connection to the proxy
	address := fmt.Sprintf("%s:%s", proxy.IP, proxy.Port)
	fmt.Println("Attempting to connect to the proxy...")

	// Retry connecting until successful (or for a limited number of retries)
	var conn net.Conn
	var err error
	for retries := 0; retries < 5; retries++ {
		conn, err = net.Dial("tcp", address)
		if err == nil {
			break
		}
		fmt.Println("Retrying connection to proxy...")
		time.Sleep(1 * time.Second)
	}

	if err != nil {
		fmt.Println("Failed to connect to proxy:", err)
		return
	}
	defer conn.Close()
	fmt.Printf("Successfully connected to proxy %s at %s\n", proxy.Name, address)

	// Example: Sending a message to the proxy
	message := "Hello from the client!"
	_, err = conn.Write([]byte(message))
	if err != nil {
		fmt.Println("Failed to send data to proxy:", err)
		return
	}
	fmt.Println("Message sent to proxy.")

	// Optionally, read a response from the proxy
	buffer := make([]byte, 1024)
	n, err := conn.Read(buffer)
	if err != nil {
		fmt.Println("Failed to read from proxy:", err)
		return
	}
	fmt.Printf("Received response from proxy: %s\n", string(buffer[:n]))
}

var (
	requests   []map[string]interface{}
	requestsMu sync.Mutex
)

func handleHTTP(w http.ResponseWriter, r *http.Request) {
	// Create a new request based on the incoming one
	req, err := http.NewRequest(r.Method, r.URL.String(), r.Body)
	if err != nil {
		log.Printf("Error during NewRequest() %s: %s\n", r.URL.String(), err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Log the request details
	logRequest(map[string]interface{}{
		"method":    r.Method,
		"url":       r.URL.String(),
		"headers":   r.Header,
		"timestamp": time.Now().UTC(),
	})

	// Copy headers from the incoming request to the new request
	for key, values := range r.Header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	// Use a default HTTP client to perform the request
	httpClient := &http.Client{}

	resp, err := httpClient.Do(req)
	if err != nil {
		log.Printf("Error during Do() %s: %s\n", r.URL.String(), err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// Copy the response from the upstream server to the client
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)

	written, err := io.Copy(w, resp.Body)
	if err != nil {
		log.Printf("Error during Copy() %s: %s\n", r.URL.String(), err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	log.Printf("%s - %s - %s - %d - %dKB\n", r.Proto, r.Method, r.Host, resp.StatusCode, written/1000)
}

func logRequest(reqDetails map[string]interface{}) {
	requestsMu.Lock()
	defer requestsMu.Unlock()

	if url, ok := reqDetails["url"].(string); ok && url == "/favicon.ico" {
		println("Skipping favicon")
		// Skip logging for /favicon.ico
		return
	}
	// Add the request to the global store
	requests = append(requests, reqDetails)
}

func handleTunnel(w http.ResponseWriter, r *http.Request) {
	// Initialize byte counters
	var clientToServerBytes, serverToClientBytes int64

	// Prepare log data
	logData := map[string]interface{}{
		"method":    r.Method,
		"url":       r.URL.Path, // Use Path instead of String for clean URL logging
		"headers":   r.Header,
		"timestamp": time.Now().UTC(),
	}
	logRequest(logData)

	if r.URL.Path == "/favicon.ico" {
		w.WriteHeader(http.StatusNoContent) // Respond with 204 No Content
		return
	}

	// Establish a TCP connection to the destination server
	dest_conn, err := net.DialTimeout("tcp", r.Host, 10*time.Second)
	if err != nil {
		log.Printf("Error connecting to destination server: %v", err)
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer dest_conn.Close()
	w.WriteHeader(http.StatusOK)

	hj, ok := w.(http.Hijacker)
	if !ok {
		log.Printf("HTTP server does not support hijacking")
		http.Error(w, "webserver doesn't support hijacking", http.StatusInternalServerError)
		return
	}

	// Hijack the source connection
	src_conn, _, err := hj.Hijack()
	if err != nil {
		log.Printf("Error hijacking source connection: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer src_conn.Close()

	// Log connection strings for debugging
	srcConnStr := fmt.Sprintf("%s->%s", src_conn.LocalAddr().String(), src_conn.RemoteAddr().String())
	dstConnStr := fmt.Sprintf("%s->%s", dest_conn.LocalAddr().String(), dest_conn.RemoteAddr().String())

	log.Printf("%s - %s - %s\n", r.Proto, r.Method, r.Host)
	log.Printf("src_conn: %s - dst_conn: %s\n", srcConnStr, dstConnStr)

	var wg sync.WaitGroup
	wg.Add(2) // We expect two goroutines to run for bi-directional data transfers

	// First goroutine for sending data from src_conn to dest_conn
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("Recovered in goroutine: %v", r)
			}
			wg.Done()
		}()
		clientToServerBytes = transfer(nil, dest_conn, src_conn, dstConnStr, srcConnStr)
	}()

	// Second goroutine for sending data from dest_conn to src_conn
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("Recovered in goroutine: %v", r)
			}
			wg.Done()
		}()
		serverToClientBytes = transfer(nil, src_conn, dest_conn, srcConnStr, dstConnStr)
	}()

	// Wait until all transfer goroutines complete
	wg.Wait()

	// Log the final request details with byte counts
	logRequest(map[string]interface{}{
		"method":           r.Method,
		"url":              r.URL.String(),
		"headers":          r.Header,
		"timestamp":        time.Now().UTC(),
		"client_to_server": clientToServerBytes,
		"server_to_client": serverToClientBytes,
	})
}

func getLoggedRequests(w http.ResponseWriter, r *http.Request) {
	requestsMu.Lock()
	defer requestsMu.Unlock()

	// Return the logged requests as JSON
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(requests); err != nil {
		http.Error(w, "Failed to encode requests", http.StatusInternalServerError)
	}
}

func transfer(stats interface{}, dst, src net.Conn, dstConnStr, srcConnStr string) int64 {
	var bytesTransferred int64
	buf := make([]byte, 32*1024) // 32KB buffer
	for {
		n, err := src.Read(buf)
		if n > 0 {
			_, writeErr := dst.Write(buf[:n])
			if writeErr != nil {
				log.Printf("Write error: %v", writeErr)
				break
			}
			bytesTransferred += int64(n)
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("Read error: %v", err)
			}
			break
		}
	}
	return bytesTransferred
}

func main() {
	// Initialize proxy clients table
	proxyClientsTable := NewProxyClientsTable()

	// Start libp2p logic
	fmt.Println("Starting libp2p logic...")

	// Create the libp2p node
	node, dht, err := createNode(proxyClientsTable)
	node2, err := createNode2(proxyClientsTable, "113455924")
	node3, err := createNode2(proxyClientsTable, "113455920")

	if err != nil {
		log.Fatalf("Failed to create node: %s", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	globalCtx = ctx

	defer node.Close()
	defer dht.Close()

	// Initialize the node, connect to relay and bootstrap peers
	connectToPeer(node, relay_node_addr)     // Connect to relay node
	makeReservation(node)                    // Make reservation on relay node
	connectToPeer(node, bootstrap_node_addr) // Connect to bootstrap node

	// Start peer exchange
	go handlePeerExchange(node)

	// Generate unique Peer IDs and private keys for proxies
	// peerID1, err := createNewPeerID()
	// if err != nil {
	// 	log.Fatalf("Failed to generate Peer ID 1: %v", err)
	// }

	fmt.Printf("Your peer MINE: %s\n", node.ID().String())
	fmt.Printf("Your peer ID1 is: %s\n", node2.ID())
	fmt.Printf("Your peer ID2 is: %s\n", node3.ID())

	// Add proxies to the table
	proxyClientsTable.AddProxyClient("proxy1", "127.0.0.1", "8085", false, node2.ID())
	proxyClientsTable.AddProxyClient("proxy2", "127.0.0.1", "8084", false, node3.ID())
	proxyClientsTable.AddProxyClient("mine", "127.0.0.1", "8082", true, node.ID())

	proxyPeerMap := map[string]peer.ID{
		"proxy1": node2.ID(),
		"proxy2": node3.ID(),
		"mine":   node.ID(),
	}

	// Start all proxies except for "mine"
	for _, proxy := range proxyClientsTable.clients {
		if proxy.IsMine {
			continue // Skip "mine"
		}

		go func(proxy ProxyClient) {
			peerID, exists := proxyPeerMap[proxy.Name]
			if !exists {
				log.Printf("No Peer ID found for proxy '%s'\n", proxy.Name)
				return
			}
			fmt.Printf("Starting proxy '%s' with Peer ID %s on %s:%s...\n", proxy.Name, peerID.String(), proxy.IP, proxy.Port)

			providerRecord := map[string]interface{}{
				"peerID": peerID.String(),
				"ip":     proxy.IP,
				"port":   proxy.Port,
			}

			providerRecordJSON, err := json.Marshal(providerRecord)
			fmt.Printf("Your PROVIDER RECORD IN MAIN %s\n", providerRecordJSON)

			if err != nil {
				log.Printf("Failed to serialize provider record for '%s': %v\n", proxy.Name, err)
				return
			}
			// Start the HTTP server for the proxy

			fmt.Printf("Starting proxy '%s' on %s:%s...\n", proxy.Name, proxy.IP, proxy.Port)
			log.Fatal(http.ListenAndServe(
				proxy.IP+":"+proxy.Port,
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodConnect {
						handleTunnel(w, r)
					} else {
						if r.URL.Path == "/logs" {
							getLoggedRequests(w, r)
						} else {
							handleHTTP(w, r)
						}

					}
				}),
			))
		}(proxy)
	}

	// Handle "mine" proxy (already in your code)
	myProxy := proxyClientsTable.GetProxyClient("mine")
	if myProxy == nil {
		fmt.Println("No proxy with the name 'mine' found")
		return
	}
	handleInput(ctx, dht, node, proxyClientsTable)
}
