package main

import (
	"context"
	"fmt"
	"net/http"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/michaelquigley/df/dl"
	"github.com/michaelquigley/pfxlog"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

func main() {
	dl.Init(dl.DefaultOptions().SetTrimPrefix("github.com/openziti/"))
	pfxlog.GlobalInit(logrus.WarnLevel, pfxlog.DefaultOptions().SetTrimPrefix("github.com/openziti/"))
	if err := newRootCommand().cmd.Execute(); err != nil {
		dl.Fatal(err)
	}
}

type rootCommand struct {
	cmd    *cobra.Command
	listen string
	cfg    config
}

func newRootCommand() *rootCommand {
	rc := &rootCommand{}
	rc.cmd = &cobra.Command{
		Use:   "dummy-keys <keyFile>",
		Short: "reference key API for testing and demos",
		Long: "dummy-keys serves the gateway's published GET /v1/keys contract from a\n" +
			"local file, so an HTTP key source can be exercised without standing up a\n" +
			"management plane. the file is re-read on every request, so editing it and\n" +
			"watching the gateway converge is the demo.\n\n" +
			"--fault injects the failure modes the gateway's design is built around;\n" +
			"they are otherwise unobservable, since they need a server that misbehaves\n" +
			"on request.",
		Args: cobra.ExactArgs(1),
		RunE: rc.run,
	}
	rc.cmd.Flags().StringVar(&rc.listen, "listen", ":8082", "listen address")
	rc.cmd.Flags().StringVar(&rc.cfg.token, "token", "", "require this bearer token; empty accepts any request")
	rc.cmd.Flags().StringVar(&rc.cfg.fault, "fault", faultNone,
		"failure to inject: "+strings.Join(faults, ", "))
	rc.cmd.Flags().IntVar(&rc.cfg.faultStatus, "fault-status", http.StatusInternalServerError,
		"status returned by --fault status")
	rc.cmd.Flags().DurationVar(&rc.cfg.stallFor, "stall-for", time.Minute,
		"how long --fault stall holds the response open")
	return rc
}

func (rc *rootCommand) run(_ *cobra.Command, args []string) error {
	rc.cfg.path = args[0]
	if !slices.Contains(faults, rc.cfg.fault) {
		return fmt.Errorf("unknown --fault '%s'; expected one of %s", rc.cfg.fault, strings.Join(faults, ", "))
	}

	srv := newServer(rc.cfg)
	// fail at startup rather than at first poll: an unreadable key file is
	// configuration this process cannot honor.
	if _, err := srv.readRecords(); err != nil {
		return fmt.Errorf("read key file '%s': %w", rc.cfg.path, err)
	}

	server := &http.Server{
		Addr:    rc.listen,
		Handler: srv.handler(),
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	dl.Infof("dummy-keys listening on '%s' serving '%s' (fault=%s)", rc.listen, rc.cfg.path, rc.cfg.fault)

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		dl.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}
