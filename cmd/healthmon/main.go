package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/alexallah/ethereum-healthmon/internal/beacon"
	"github.com/alexallah/ethereum-healthmon/internal/common"
	"github.com/alexallah/ethereum-healthmon/internal/execution"
	"github.com/jessevdk/go-flags"
)

type Options struct {
	// Aggiunta la scelta "avax"
	Chain   string `long:"chain" description:"Chain type" choice:"execution" choice:"beacon" choice:"avax" required:"true"`
	Port    int    `long:"port" description:"Node port (default: 8545 for execution, 9650 for avax, 4000 for beacon)"`
	Addr    string `long:"addr" description:"Node host address" default:"localhost"`
	Timeout int64  `long:"timeout" description:"Node connection timeout, seconds" default:"5"`

	Execution struct {
		Jwt           string `long:"engine-jwt" description:"JWT hex secret path. Use only when connecting to the engine RPC endpoint."`
		SyncTolerance uint64 `long:"sync-tolerance" description:"Max block lag tolerance while syncing." default:"0"`
	} `group:"Execution chain" namespace:"execution"`

	Beacon struct {
		Certificate string `long:"certificate" description:"TLS root certificate path. Specify only if you have it configured for your node as well."`
	} `group:"Beacon chain" namespace:"beacon"`

	Service struct {
		Port int    `long:"port" description:"healthmon listening port" default:"21171"`
		Addr string `long:"addr" description:"healthmon listening address. Set to 0.0.0.0 to allow external access." default:"localhost"`
	} `group:"healthcheck service" namespace:"http"`
}

var state *common.State

func main() {
	// Log puliti per systemd
	log.SetFlags(0)

	var opts Options
	parser := flags.NewParser(&opts, flags.Default)
	_, err := parser.Parse()
	if err != nil {
		os.Exit(0)
	}

	state = new(common.State)

	// Definizione Porta di Default in base alla chain
	nodePort := opts.Port
	if nodePort == 0 {
		switch opts.Chain {
			case "beacon":
				nodePort = 4000
			case "avax":
				nodePort = 9650
			default: // execution
				nodePort = 8545
		}
	}

	// Costruzione dell'indirizzo del nodo
	var nodeAddr string

	switch opts.Chain {
		case "avax":
			nodeAddr = fmt.Sprintf("http://%s:%d/ext/bc/C/rpc", opts.Addr, nodePort)

			execution.StartUpdater(state, nodeAddr, opts.Timeout, opts.Execution.Jwt, opts.Execution.SyncTolerance)

		case "execution":
			nodeAddr = fmt.Sprintf("%s:%d", opts.Addr, nodePort)
			execution.StartUpdater(state, nodeAddr, opts.Timeout, opts.Execution.Jwt, opts.Execution.SyncTolerance)

		case "beacon":
			nodeAddr = fmt.Sprintf("%s:%d", opts.Addr, nodePort)
			beacon.StartUpdater(state, nodeAddr, opts.Timeout, opts.Beacon.Certificate)

		default:
			log.Fatalf("unknown chain: %s.\n", opts.Chain)
	}

	log.Printf("%s node address is %s", opts.Chain, nodeAddr)
	serviceAddr := fmt.Sprintf("%s:%d", opts.Service.Addr, opts.Service.Port)
	log.Printf("healthmon listening on %s\n", serviceAddr)

	http.HandleFunc("/ready", statusHandler)
	http.HandleFunc("/metrics", metricsHandler)
	http.ListenAndServe(serviceAddr, nil)
}

func statusHandler(w http.ResponseWriter, req *http.Request) {
	if state.IsHealthy() {
		io.WriteString(w, "OK")
	} else {
		w.WriteHeader(503)
		io.WriteString(w, "NOT READY")
	}
}

func metricsHandler(w http.ResponseWriter, req *http.Request) {
	var ready int
	if state.IsHealthy() {
		ready = 1
	}
	io.WriteString(w, fmt.Sprintf("# TYPE ready gauge\nready %d\n", ready))
}
