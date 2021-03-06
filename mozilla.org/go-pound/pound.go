package main

import (
	"code.google.com/p/go.net/websocket"
	"encoding/json"
	"flag"
	"log"
	"os"
	"runtime"
	"time"
	"net"
)

const (
	VERSION = "0.0.2"
)

// config cruft
type Config struct {
	Target   string
	Deadline string
	Clients  int
	Sleep    string
}

// Wrapper funcs for websocket
func DialTimeout(url_, protocol, origin string, timeout time.Duration) (ws *websocket.Conn, err error) {
	config, err := websocket.NewConfig(url_, origin)
	if err != nil {
		return nil, err
	}
	if protocol != "" {
		config.Protocol = []string{protocol}
	}
	return DialConfigTimeout(config, timeout)
}

func DialConfigTimeout(config *websocket.Config, timeout time.Duration) (ws *websocket.Conn, err error) {
	var client net.Conn
	if config.Location == nil {
		return nil, &websocket.DialError{config, websocket.ErrBadWebSocketLocation}
	}
	if config.Origin == nil {
		return nil, &websocket.DialError{config, websocket.ErrBadWebSocketOrigin}
	}
	switch config.Location.Scheme {
	case "ws":
		client, err = net.DialTimeout("tcp", config.Location.Host, timeout)

	// tls.go doesn't support DialTimeout so we don't support it
	default:
		err = websocket.ErrBadScheme
	}
	if err != nil {
		goto Error
	}

	ws, err = websocket.NewClient(config, client)
	if err != nil {
		goto Error
	}
	return

Error:
	return nil, &websocket.DialError{config, err}

}

func parseConfig(filename string) (config *Config) {
	config = new(Config)
	file, err := os.Open(filename)
	if err != nil {
		log.Fatal("unable to open file: " + err.Error())
	}
	defer file.Close()
	fileInfo, err := file.Stat()
	fsize := fileInfo.Size()
	rawBytes := make([]byte, fsize)
	_, err = file.Read(rawBytes)
	if err != nil {
		log.Fatal("unable to read file: " + err.Error())
	}
	if err = json.Unmarshal(rawBytes, config); err != nil {
		log.Fatal("Unable to parse file: " + err.Error())
	}

	return config
}

func poundSock(target string, config *Config, cmd, ctrl chan int, id int) (err error) {
	hostname, err := os.Hostname()
	log.Printf("INFO : (%d) Connecting to %s\n", id, target)
	//ws, err := websocket.Dial(config.Target, "", "http://"+hostname)
	dur, err := time.ParseDuration(config.Deadline)
	ws, err := DialTimeout(config.Target, "push-notification", "http://"+hostname, dur)
	if err != nil {
		log.Printf("ERROR: (%d) Unable to open websocket: %s\n",
			id, err.Error())
		cmd <- id
		return err
	}
	msg := make([]byte, 512)
	duration, err := time.ParseDuration(config.Sleep)
	tc := time.NewTicker(duration)
	for {
		_, err = ws.Write([]byte("{\"messageType\":\"ping\"}"))
		if err != nil {
			log.Printf("ERROR: (%d) Unable to write ping to websocket %s\n",
				id, err.Error())
			cmd <- id
			return err
		}
		msg = msg[:0]
		if _, err = ws.Read(msg); err != nil {
			log.Printf("WARN : (%d) Bad response %s\n", id, err.Error())
			cmd <- id
			return
		}
		select {
		case cc := <-ctrl:
			if cc == 0 {
				break
			}
		case <-tc.C:
			continue
		}
	}
	log.Printf("INFO : (%d) Shutting down...\n", id)
	return err
}

func main() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("PANIC! %s\n", r)
		}
	}()
	configFile := flag.String("config", "config.json", "Config file")
	flag.Parse()

	config := parseConfig(*configFile)
	if config == nil {
		log.Fatal("No config")
		return
	}

	// This is an odd value full of voodoo.
	// The docs say that this should match the number of CPUs, only if you
	// set it to 1, go appears to not actually spawn any threads. (None of
	// the poundSock() calls are made.) If you give it something too excessive,
	// the scheduler blows chunks. 8 per CPU, while fairly arbitrary, seems
	// to provide the greatest stability.
	//
	// Go is a fun toy, but this is why you don't build hospitals out of lego.
	runtime.GOMAXPROCS(runtime.NumCPU() * 8)

	chans := make(map[int]chan int)
	cmd := make(chan int, config.Clients)

	// run as many clients as specified
	totalClients := config.Clients
	for cli := 0; cli < totalClients; cli++ {

		ctrl := make(chan int)
		chans[cli] = ctrl
		// open a socket to the Target
		log.Printf("Spawning %d\n", cli)

		go func(cli int) {
			poundSock(config.Target, config, cmd, ctrl, cli)
		}(cli)
	}
	lastTot := runtime.NumGoroutine()
	tc := time.NewTicker(time.Duration(time.Second * 5))
	for {
		select {
		case x := <-cmd:
			log.Printf("Exiting %d \n", x)
			totalClients = runtime.NumGoroutine()
		case <-tc.C:
			if totalClients != lastTot {
				log.Printf("Info: Active Clients: %d \n", totalClients)
				lastTot = totalClients
			}
		}
	}
}
