package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/hashicorp/go-multierror"

	"github.com/melonproject/parity-go-client"

	metrics "github.com/armon/go-metrics"
	"github.com/armon/go-metrics/prometheus"

	promClient "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	gracefulTimeout = 5 * time.Second
)

var (
	DefaultHTTPAddr = &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 4647}
)

type Config struct {
	LogOutput  io.Writer
	HTTPAddr   *net.TCPAddr
	ParityAddr string
	NodeName   string
}

func DefaultConfig() *Config {
	c := &Config{
		LogOutput:  os.Stderr,
		HTTPAddr:   DefaultHTTPAddr,
		NodeName:   "parity",
		ParityAddr: "localhost:8545",
	}

	hostname, err := os.Hostname()
	if err == nil {
		c.NodeName = hostname
	}

	return c
}

type Exporter struct {
	config    *Config
	logger    *log.Logger
	InmemSink *metrics.InmemSink

	parityClient *parityclient.Client
	rcpStop      chan struct{}
	mux          *http.ServeMux
	listener     net.Listener
}

func NewExporter(config *Config) (*Exporter, error) {
	e := &Exporter{
		config:  config,
		rcpStop: make(chan struct{}, 1),
	}

	e.logger = log.New(config.LogOutput, "", log.LstdFlags)

	var err error
	e.InmemSink, err = e.setupTelemetry()
	if err != nil {
		return nil, err
	}

	return e, nil
}

func (e *Exporter) setupTelemetry() (*metrics.InmemSink, error) {

	inm := metrics.NewInmemSink(10*time.Second, time.Minute)
	metrics.DefaultInmemSignal(inm)

	metricsConf := metrics.DefaultConfig("parity")
	metricsConf.EnableHostname = true
	metricsConf.HostName = e.config.NodeName

	promSink, err := prometheus.NewPrometheusSink()
	if err != nil {
		return inm, nil
	}

	metrics.NewGlobal(metricsConf, promSink)
	return inm, nil
}

func (e *Exporter) Start() error {
	e.logger.Println("Staring server")

	err := e.startHttp()
	if err != nil {
		return err
	}

	go e.startRPC()

	return nil
}

func (e *Exporter) startHttp() error {

	l, err := net.Listen("tcp", e.config.HTTPAddr.String())
	if err != nil {
		return fmt.Errorf("failed to start listner on %s: %v", e.config.HTTPAddr.String(), err)
	}

	e.listener = l

	e.mux = http.NewServeMux()
	e.mux.Handle("/metrics", e.wrap(e.MetricsRequest))

	go http.Serve(l, e.mux)

	e.logger.Printf("Http api running on %s", e.config.HTTPAddr.String())

	return nil
}

func (e *Exporter) wrap(handler func(resp http.ResponseWriter, req *http.Request) (interface{}, error)) http.HandlerFunc {
	return func(resp http.ResponseWriter, req *http.Request) {
		handleErr := func(err error) {
			resp.WriteHeader(http.StatusInternalServerError)
			resp.Write([]byte(err.Error()))
		}

		obj, err := handler(resp, req)
		if err != nil {
			handleErr(err)
			return
		}

		if obj == nil {
			return
		}

		buf, err := json.Marshal(obj)
		if err != nil {
			handleErr(err)
			return
		}

		resp.Header().Set("Content-Type", "application/json")
		resp.Write(buf)
	}
}

func (e *Exporter) startRPC() {
	e.parityClient = parityclient.NewClient(e.config.ParityAddr)

	for {
		select {
		case <-time.After(5 * time.Second):
			err := e.rpcCalls()
			if err != nil {
				e.logger.Printf("[ERR]: Failed to make rpc calls to parity: %v", err)
			}

		case <-e.rcpStop:
			return
		}
	}
}

func (e *Exporter) rpcCalls() error {
	var errors error

	// Peers

	peers, err := e.parityClient.Peers()
	if err != nil {
		errors = multierror.Append(errors, err)
	} else {
		e.InmemSink.SetGauge([]string{"peers"}, float32(peers.Connected))
	}

	return errors
}

func (e *Exporter) MetricsRequest(resp http.ResponseWriter, req *http.Request) (interface{}, error) {
	if req.Method != "GET" {
		return nil, fmt.Errorf("Incorrect method. Found %s, only GET available", req.Method)
	}

	if format := req.URL.Query().Get("format"); format == "prometheus" {
		handlerOptions := promhttp.HandlerOpts{
			ErrorLog:           e.logger,
			ErrorHandling:      promhttp.ContinueOnError,
			DisableCompression: true,
		}

		handler := promhttp.HandlerFor(promClient.DefaultGatherer, handlerOptions)
		handler.ServeHTTP(resp, req)
		return nil, nil
	}

	return e.InmemSink.DisplayMetrics(resp, req)
}

func (e *Exporter) Shutdown() error {
	e.logger.Println("Shutting down")

	e.listener.Close()
	e.rcpStop <- struct{}{}

	return nil
}

func readConfig(args []string) (*Config, error) {
	return nil, nil
}

func main() {
	flag.Parse()

	config := DefaultConfig()
	logger := log.New(config.LogOutput, "", log.LstdFlags)

	// Handle interupts.
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)

	exporter, err := NewExporter(config)
	if err != nil {
		logger.Printf("[ERR]: Failed to create the exporter: %v", err)
		os.Exit(1)
	}

	if err := exporter.Start(); err != nil {
		logger.Printf("[ERR]: Failed to start the exporter: %v", err)
		os.Exit(1)
	}

	for range c {
		exporter.Shutdown()
		return
	}
}
