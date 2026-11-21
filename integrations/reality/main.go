package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"chronomorph/internal/bridgeconfig"
	"chronomorph/internal/realitytransport"
)

func run(output io.Writer) error {
	input := bufio.NewReader(os.Stdin)
	line, err := input.ReadSlice('\n')
	if err != nil {
		return errors.New("invalid bridge configuration")
	}
	var cfg bridgeconfig.Config
	if err := json.Unmarshal(line, &cfg); err != nil || len(cfg.Key) != 32 || len(cfg.ShortID) != 8 {
		return errors.New("invalid bridge identity")
	}
	switch cfg.Mode {
	case "client":
		secured, err := realitytransport.Dial(context.Background(), cfg)
		if err != nil {
			return err
		}
		defer secured.Close()
		done := make(chan struct{}, 2)
		go func() { _, _ = io.Copy(secured, input); done <- struct{}{} }()
		go func() { _, _ = io.Copy(output, secured); done <- struct{}{} }()
		<-done
		return nil
	case "server":
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() { _, _ = io.Copy(io.Discard, input); cancel() }()
		err := realitytransport.Serve(ctx, cfg, func() error {
			_, err := io.WriteString(output, "READY\n")
			return err
		})
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	default:
		return errors.New("unknown bridge mode")
	}
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "version" {
		fmt.Println("ChronoMorph REALITY bridge 1 (XTLS/Xray-core v26.11.9)")
		return
	}
	// Keep upstream startup and diagnostic messages off the binary data pipe.
	output := os.Stdout
	os.Stdout = os.Stderr
	if err := run(output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
