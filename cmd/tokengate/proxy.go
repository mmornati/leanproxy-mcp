package main

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

var proxyCmd = &cobra.Command{
	Use:   "proxy",
	Short: "Manage proxy server",
	Long:  `Manage the tokengate proxy server including start, stop, and status commands.`,
}

var proxyStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the proxy server",
	Long:  `Start the tokengate proxy server on the specified address.`,
	RunE:  runProxyStart,
}

var proxyFlags struct {
	ListenAddr  string
	UpstreamURL string
}

func init() {
	proxyCmd.AddCommand(proxyStartCmd)
	proxyStartCmd.Flags().StringVar(&proxyFlags.ListenAddr, "listen", "127.0.0.1:8080", "Address to listen on")
	proxyStartCmd.Flags().StringVar(&proxyFlags.UpstreamURL, "upstream", "http://localhost:8081", "Upstream JSON-RPC server URL")
}

func runProxyStart(cmd *cobra.Command, args []string) error {
	configPath := GlobalConfigPath
	if configPath == "" {
		configPath = os.Getenv("TOKENGATE_CONFIG")
	}

	if configPath != "" {
		fmt.Fprintf(os.Stderr, "tokengate: loading config from %s\n", configPath)
	}

	if DryRunEnabled {
		fmt.Println("Dry-run mode: would start proxy server")
		fmt.Printf("  Listen: %s\n", proxyFlags.ListenAddr)
		fmt.Printf("  Upstream: %s\n", proxyFlags.UpstreamURL)
		return nil
	}

	fmt.Printf("tokengate: starting proxy server on %s\n", proxyFlags.ListenAddr)

	ln, err := net.Listen("tcp", proxyFlags.ListenAddr)
	if err != nil {
		ExitWithError(ExitGeneral, err)
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		fmt.Fprintln(os.Stderr, "tokengate: shutting down proxy server")
		ln.Close()
		os.Exit(ExitSuccess)
	}()

	fmt.Printf("tokengate: proxy server ready on %s\n", ln.Addr().String())

	for {
		conn, err := ln.Accept()
		if err != nil {
			continue
		}
		go handleProxyConnection(conn)
	}
}

func handleProxyConnection(conn net.Conn) {
	defer conn.Close()
	conn.Write([]byte("tokengate proxy active\n"))
}