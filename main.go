package main

import (
	"errors"
	"flag"
	"github.com/fsouza/go-dockerclient"
	"io"
	"log"
	"net"
	"os"
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

	wg.Wait()
}
