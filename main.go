package main

import (
	"encoding/json"
	"errors"
	"flag"
	"github.com/coreos/go-etcd/etcd"
	"github.com/fsouza/go-dockerclient"
	"io"
	"log"
	"net"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
)

var (
	hostIP       string
	etcdMachines []string
)

func getopt(name, def string) string {
	if env := os.Getenv(name); env != "" {
		return env
	}
	return def
}

func assert(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func proxyConn(conn net.Conn, addr string) {
	backend, err := net.Dial("tcp", addr)
	defer conn.Close()
	if err != nil {
		log.Println("proxy", err.Error())
		return
	}
	defer backend.Close()

	done := make(chan struct{})
	go func() {
		io.Copy(backend, conn)
		backend.(*net.TCPConn).CloseWrite()
		close(done)
	}()
	io.Copy(conn, backend)
	conn.(*net.TCPConn).CloseWrite()
	<-done
}

func findBackend(conn net.Conn, destPort int) string {
	ip, _, _ := net.SplitHostPort(conn.RemoteAddr().String())
	name, err := inspectBackendName(ip, strconv.Itoa(destPort))
	if err != nil {
		log.Println("findBackend:", err)
	}
	return lookupSRV(name)
}

func lookupSRV(name string) string {
	log.Printf("lookupSRV: name=%s", name)
	_, addrs, err := net.LookupSRV("", "", name)
	if err != nil {
		log.Println("dns:", err)
		return ""
	}
	if len(addrs) == 0 {
		return ""
	}
	port := strconv.Itoa(int(addrs[0].Port))
	return net.JoinHostPort(addrs[0].Target, port)
}

func inspectBackendName(sourceIP, destPort string) (string, error) {
	log.Printf("inspecting ip=%s port=%s", sourceIP, destPort)
	envKey := "BACKENDS_" + destPort

	client, _ := docker.NewClient(getopt("DOCKER_HOST", "unix:///var/run/docker.sock"))

	containers, err := client.ListContainers(docker.ListContainersOptions{})
	if err != nil {
		return "", errors.New("unable to list containers")
	}
	for _, listing := range containers {
		container, err := client.InspectContainer(listing.ID)
		if err != nil {
			return "", errors.New("unable to inspect container " + listing.ID)
		}
		if container.NetworkSettings.IPAddress == sourceIP {
			for _, env := range container.Config.Env {
				parts := strings.SplitN(env, "=", 2)
				if strings.ToLower(parts[0]) == strings.ToLower(envKey) {
					return parts[1], nil
				}
			}
			return "", errors.New("backend not found in container environment")
		}
	}
	return "", errors.New("unable to find container with source IP")
}

type service struct {
	Name     string
	App      string
	Port     string
	HostPort string
}

type Announcement struct {
	Host     string `json:"host"`
	Port     int    `json:"port,omitempty"`
	Priority int    `json:"priority,omitempty"`
	Weight   int    `json:"weight,omitempty"`
	TTL      int    `json:"ttl,omitempty"`
}

func srvDomains(container *docker.Container) ([]*service, error) {
	serviceMap := map[string]string{}
	var appName string
	log.Println(container.HostConfig.PortBindings)
	for _, e := range container.Config.Env {
		parts := strings.SplitN(e, "=", 2)
		switch parts[0] {
		case "DOKKAA_APP_NAME":
			appName = parts[1]
		}
		if strings.HasPrefix(parts[0], "DOKKAA_SERVICE_") {
			name := strings.TrimPrefix(parts[0], "DOKKAA_SERVICE_")
			name = strings.ToLower(name)
			serviceMap[name] = parts[1]
		}
	}
	log.Println(serviceMap)

	var services []*service
	for name, port := range serviceMap {
		hostPort := container.HostConfig.PortBindings[docker.Port(port+"/tcp")][0].HostPort
		hostPort = strings.TrimSuffix(hostPort, "/tcp")
		hostPort = strings.TrimSuffix(hostPort, "/udp")
		service := &service{
			App:      appName,
			Name:     name,
			Port:     port,
			HostPort: hostPort,
		}
		services = append(services, service)
	}

	return services, nil
}

func registerService(container *docker.Container) error {
	services, err := srvDomains(container)
	if err != nil {
		log.Println("can't inspect services: ", err)
		return err
	}
	cli := etcd.NewClient(etcdMachines)
	base := path.Join("/", "skydns", "local", "skydns")
	for _, s := range services {
		path := path.Join(base, s.App, s.Name)
		port, _ := strconv.Atoi(s.HostPort)
		ann := &Announcement{
			Host: hostIP,
			Port: port,
		}
		value, _ := json.Marshal(ann)
		cli.Set(path, string(value), 0)
	}
	return nil
}

func deleteService(container *docker.Container) error {
	return nil
}

func startDockerEventLoop() {
	client, _ := docker.NewClient(getopt("DOCKER_HOST", "unix:///var/run/docker.sock"))
	c := make(chan *docker.APIEvents)
	client.AddEventListener(c)
	for event := range c {
		log.Println("event received: ", event)
		container, err := client.InspectContainer(event.ID)
		if err != nil {
			log.Println("can't inspect container: ", err)
			continue
		}
		switch event.Status {
		case "start":
			registerService(container)
		case "die":
			deleteService(container)
		}
	}
}

func main() {
	flag.Parse()
	hostIP = getopt("HOST_IP", "127.0.0.1")
	portRange := strings.Split(getopt("PORT_RANGE", "10000:10100"), ":")
	pStart, _ := strconv.Atoi(portRange[0])
	pEnd, _ := strconv.Atoi(portRange[1])
	log.Printf("Listening on ports from %d through %d", pStart, pEnd)
	var wg sync.WaitGroup
	wg.Add(pEnd - pStart + 1)
	for i := pStart; i <= pEnd; i++ {
		go func(port int) {
			defer wg.Done()
			listener, err := net.Listen("tcp", ":"+strconv.Itoa(port))
			assert(err)
			for {
				conn, err := listener.Accept()
				if err != nil {
					log.Fatal(err)
				}

				backend := findBackend(conn, port)
				if backend == "" {
					conn.Close()
					log.Printf("No backends! Closing the connection")
					continue
				}

				go proxyConn(conn, backend)
			}
		}(i)
	}

	go startDockerEventLoop()

	wg.Wait()
}
