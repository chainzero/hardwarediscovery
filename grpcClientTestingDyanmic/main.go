package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"google.golang.org/grpc"

	"github.com/gorilla/mux"

	v1 "github.com/akash-network/provider/operator/inventory/v1"
)

const (
	daemonSetLabelSelector = "app=hostfeaturediscovery"
	daemonSetNamespace     = "akash-services"
	grpcPort               = ":50051"
	nodeUpdateInterval     = 5 * time.Second // Duration after which to print the cluster state
	Added                  = "ADDED"
	Deleted                = "DELETED"
)

var instance *ConcurrentClusterData
var once sync.Once

// ConcurrentClusterData provides a concurrency-safe way to store and update cluster data.
type ConcurrentClusterData struct {
	sync.RWMutex
	cluster    *v1.Cluster
	podNodeMap map[string]int // Map of pod UID to node index in the cluster.Nodes slice
}

// NewConcurrentClusterData initializes a new instance of ConcurrentClusterData with empty cluster data.
func NewConcurrentClusterData() *ConcurrentClusterData {
	return &ConcurrentClusterData{
		cluster:    &v1.Cluster{Nodes: []v1.Node{}},
		podNodeMap: make(map[string]int),
	}
}

// UpdateNode updates or adds the node to the cluster data.
func (ccd *ConcurrentClusterData) UpdateNode(podUID string, node *v1.Node) {
	ccd.Lock()
	defer ccd.Unlock()

	if nodeIndex, ok := ccd.podNodeMap[podUID]; ok {
		// Node exists, update it
		ccd.cluster.Nodes[nodeIndex] = *node
	} else {
		// Node does not exist, add it
		ccd.cluster.Nodes = append(ccd.cluster.Nodes, *node)
		ccd.podNodeMap[podUID] = len(ccd.cluster.Nodes) - 1
	}
}

func (ccd *ConcurrentClusterData) RemoveNode(podUID string) {
	ccd.Lock()
	defer ccd.Unlock()

	if nodeIndex, ok := ccd.podNodeMap[podUID]; ok {
		// Remove the node from the slice
		ccd.cluster.Nodes = append(ccd.cluster.Nodes[:nodeIndex], ccd.cluster.Nodes[nodeIndex+1:]...)
		delete(ccd.podNodeMap, podUID) // Remove the entry from the map

		// Update the indices in the map
		for podUID, index := range ccd.podNodeMap {
			if index > nodeIndex {
				ccd.podNodeMap[podUID] = index - 1
			}
		}
	}
}

// Helper function to perform a deep copy of the Cluster struct.
func deepCopy(cluster *v1.Cluster) *v1.Cluster {
	if cluster == nil {
		return nil
	}

	if len(cluster.Nodes) == 0 {
		// Log a warning instead of returning an error
		log.Printf("Warning: Attempting to deep copy a cluster with an empty Nodes slice")
	}

	// Create a new Cluster instance
	copied := &v1.Cluster{}

	// Deep copy each field from the original Cluster to the new instance
	// Deep copy the Nodes slice
	copied.Nodes = make([]v1.Node, len(cluster.Nodes))
	for i, node := range cluster.Nodes {
		// Assuming Node is a struct, create a copy
		// If Node contains slices or maps, this process needs to be recursive
		copiedNode := node // This is a shallow copy, adjust as needed
		copied.Nodes[i] = copiedNode
	}

	fmt.Println("copied struct before return in deepCopy: ", copied)

	return copied
}

func watchPods(clientset *kubernetes.Clientset, stopCh <-chan struct{}, clusterData *ConcurrentClusterData) error {
	watcher, err := clientset.CoreV1().Pods(daemonSetNamespace).Watch(context.TODO(), metav1.ListOptions{
		LabelSelector: daemonSetLabelSelector,
	})
	if err != nil {
		return fmt.Errorf("error setting up Kubernetes watcher: %w", err)
	}
	defer watcher.Stop()

	for {
		select {
		case <-stopCh:
			log.Println("Stopping pod watcher")
			return nil
		case event, ok := <-watcher.ResultChan():
			if !ok {
				return fmt.Errorf("watcher channel closed unexpectedly")
			}

			pod, ok := event.Object.(*corev1.Pod)
			if !ok {
				log.Println("Unexpected type in watcher event")
				continue
			}

			switch event.Type {
			case Added:
				if pod.Status.Phase == corev1.PodRunning && pod.Status.PodIP != "" {
					go connectToGrpcStream(pod, clusterData) // Consider handling errors from goroutines
				} else {
					go waitForPodReadyAndConnect(clientset, pod, clusterData) // Consider handling errors from goroutines
				}
			case Deleted:
				clusterData.RemoveNode(string(pod.UID))
				log.Printf("Pod deleted: %s, UID: %s\n", pod.Name, pod.UID)
			}
		}
	}
}

// waitForPodReadyAndConnect waits for a pod to become ready before attempting to connect to its gRPC stream
func waitForPodReadyAndConnect(clientset *kubernetes.Clientset, pod *corev1.Pod, clusterData *ConcurrentClusterData) {
	for {
		// Polling the pod's status periodically
		time.Sleep(2 * time.Second)
		currentPod, err := clientset.CoreV1().Pods(pod.Namespace).Get(context.TODO(), pod.Name, metav1.GetOptions{})
		if err != nil {
			log.Printf("Error getting pod status: %v\n", err)
			continue
		}

		if currentPod.Status.Phase == corev1.PodRunning && currentPod.Status.PodIP != "" {
			// Pod is now running and has an IP, we can connect to it
			connectToGrpcStream(currentPod, clusterData) // Pass clusterData here
			return
		}
	}
}

func connectToGrpcStream(pod *corev1.Pod, clusterData *ConcurrentClusterData) {
	ipAddress := fmt.Sprintf("%s%s", pod.Status.PodIP, grpcPort)
	fmt.Println("Connecting to:", ipAddress)

	// Establish the gRPC connection
	conn, err := grpc.Dial(ipAddress, grpc.WithInsecure(), grpc.WithBlock())
	if err != nil {
		log.Printf("did not connect to pod IP %s: %v", pod.Status.PodIP, err)
		return
	}
	defer conn.Close()

	client := v1.NewMsgClient(conn)

	// Create a stream to receive updates from the node
	stream, err := client.QueryNode(context.Background(), &v1.VoidNoParam{})
	if err != nil {
		log.Printf("could not query node for pod IP %s: %v", pod.Status.PodIP, err)
		return
	}

	for {
		node, err := stream.Recv()
		if err != nil {
			// Handle stream error
			clusterData.RemoveNode(string(pod.UID))
			log.Printf("Stream closed for pod UID %s: %v", pod.UID, err)
			break
		}

		// Update the node information in the cluster data
		clusterData.UpdateNode(string(pod.UID), node)
	}
}

func printCluster() {
	// Retrieve a deep copy of the current cluster state
	cluster := GetCurrentClusterState()

	// If no nodes to print, just return
	if len(cluster.Nodes) == 0 {
		fmt.Println("No nodes in the cluster.")
		return
	}

	// Print the cluster state
	fmt.Printf("Cluster State: %+v\n\n\n", cluster)
}

func FeatureDiscovery() {
	fmt.Println("Starting up gRPC client...")

	// Use in-cluster configuration
	config, err := rest.InClusterConfig()
	if err != nil {
		log.Fatalf("Error obtaining in-cluster config: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatalf("Error creating Kubernetes client: %v", err)
	}

	clusterData := GetInstance()

	// Start the watcher in a goroutine with error handling
	errCh := make(chan error, 1)
	stopCh := make(chan struct{})
	go func() {
		defer close(errCh) // Ensure the channel is closed properly
		if err := watchPods(clientset, stopCh, clusterData); err != nil {
			errCh <- err
		}
	}()

	// Handle errors in a separate non-blocking goroutine
	go func() {
		for err := range errCh {
			log.Printf("Error from watchPods: %v", err)
			// Additional error handling logic here
		}
	}()

	// The select statement for handling errors
	select {
	case err, ok := <-errCh:
		if !ok {
			// errCh has been closed, handle this if necessary
			break
		}
		// Handle error
		log.Printf("Error from watchPods: %v", err)
	}

	// Start a ticker to periodically check/print the cluster state
	ticker := time.NewTicker(nodeUpdateInterval)
	go func() {
		for range ticker.C {
			printCluster() // Ensure this function uses clusterData
		}
	}()

	// API endpoint which serves feature disxovery data to Akash Provider
	router := mux.NewRouter()
	router.HandleFunc("/getClusterState", getClusterStateHandler).Methods("GET")

	log.Fatal(http.ListenAndServe(":8080", router))

	// Block the main function from exiting
	// Remove when running into larger code base - this is only necessary when running code in isolation
	<-make(chan struct{})
}

// GetInstance returns the singleton instance of ConcurrentClusterData.
func GetInstance() *ConcurrentClusterData {
	once.Do(func() {
		log.Println("Initializing ConcurrentClusterData instance")
		instance = &ConcurrentClusterData{
			cluster:    &v1.Cluster{Nodes: []v1.Node{}},
			podNodeMap: make(map[string]int),
		}
	})
	return instance
}

// GetCurrentClusterState returns a deep copy of the current state of the cluster and is used primarily for API GET data
func GetCurrentClusterState() *v1.Cluster {
	// Use the singleton instance to get the cluster
	clusterData := GetInstance()

	log.Printf("Current cluster state before deep copy: %+v", clusterData.cluster)

	// Return a deep copy of the cluster
	return deepCopy(clusterData.cluster)
}

func getClusterStateHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Println("###within getClusterStateHandler")
	clusterState := GetCurrentClusterState()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(clusterState); err != nil {
		log.Printf("Error encoding response: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
	}
}
