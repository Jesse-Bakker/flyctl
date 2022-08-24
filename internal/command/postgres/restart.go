package postgres

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/superfly/flyctl/agent"
	"github.com/superfly/flyctl/api"
	"github.com/superfly/flyctl/client"
	"github.com/superfly/flyctl/flaps"
	"github.com/superfly/flyctl/flypg"
	"github.com/superfly/flyctl/internal/app"
	"github.com/superfly/flyctl/internal/command"
	"github.com/superfly/flyctl/internal/flag"
	"github.com/superfly/flyctl/iostreams"
)

func newRestart() *cobra.Command {
	const (
		short = "Restarts each member of the Postgres cluster one by one."
		long  = short + " Downtime should be minimal." + "\n"
		usage = "restart"
	)

	cmd := command.New(usage, short, long, runRestart,
		command.RequireSession,
		command.RequireAppName,
	)

	flag.Add(cmd,
		flag.App(),
		flag.AppConfig(),
	)

	return cmd
}

func runRestart(ctx context.Context) error {
	var (
		MinPostgresHaVersion = "0.0.20"
		client               = client.FromContext(ctx).API()
		appName              = app.NameFromContext(ctx)
	)

	app, err := client.GetAppCompact(ctx, appName)
	if err != nil {
		return fmt.Errorf("get app: %w", err)
	}

	if !app.IsPostgresApp() {
		return fmt.Errorf("app %s is not a Postgres app", app.Name)
	}

	switch app.PlatformVersion {
	case "nomad":
		if err := hasRequiredVersionOnNomad(app, MinPostgresHaVersion, MinPostgresHaVersion); err != nil {
			return err
		}
		return restartNomadCluster(ctx, app)
	case "machines":
		agentclient, err := agent.Establish(ctx, client)
		if err != nil {
			return fmt.Errorf("can't establish agent %w", err)
		}

		dialer, err := agentclient.Dialer(ctx, app.Organization.Slug)
		if err != nil {
			return fmt.Errorf("can't build tunnel for %s: %s", app.Organization.Slug, err)
		}

		leader, err := fetchLeader(ctx, app, dialer)
		if err != nil {
			return fmt.Errorf("can't fetch leader: %w", err)
		}
		if err := hasRequiredVersionOnMachines(leader, MinPostgresHaVersion, MinPostgresHaVersion); err != nil {
			return err
		}
		return restartMachinesCluster(ctx, app)
	}

	return nil
}

func restartMachinesCluster(ctx context.Context, app *api.AppCompact) error {
	var (
		client = client.FromContext(ctx).API()
		io     = iostreams.FromContext(ctx)
	)

	flapsClient := flaps.FromContext(ctx)

	if err := flapsClient.EstablishForApp(ctx, app); err != nil {
		return err
	}

	// map of machine lease to machine
	machines := make(map[string]*api.Machine)

	out, err := flapsClient.List(ctx, "started")
	if err != nil {
		return fmt.Errorf("machines could not be retrieved %w", err)
	}

	fmt.Fprintf(io.Out, "Acquiring lease on postgres cluster\n")

	for _, machine := range out {
		lease, err := flapsClient.GetLease(ctx, machine.ID, api.IntPointer(40))
		if err != nil {
			return fmt.Errorf("failed to obtain lease: %w", err)
		}
		machines[lease.Data.Nonce] = machine
	}

	if len(out) == 0 {
		return fmt.Errorf("no machines found")
	}

	agentclient, err := agent.Establish(ctx, client)
	if err != nil {
		return fmt.Errorf("can't establish agent %w", err)
	}

	dialer, err := agentclient.Dialer(ctx, app.Organization.Slug)
	if err != nil {
		return fmt.Errorf("can't build tunnel for %s: %s", app.Organization.Slug, err)
	}

	fmt.Fprintf(io.Out, "Restarting Postgres\n")

	for lease, machine := range machines {
		fmt.Fprintf(io.Out, " Restarting %s with lease %s\n", machine.ID, lease)

		pgclient := flypg.NewFromInstance(fmt.Sprintf("[%s]", machine.PrivateIP), dialer)

		if err := pgclient.RestartNodePG(ctx); err != nil {
			return fmt.Errorf("failed to restart postgres on node: %w", err)
		}
	}

	fmt.Fprintf(io.Out, "Restart complete\n")

	return nil
}

func restartNomadCluster(ctx context.Context, app *api.AppCompact) (err error) {
	var (
		client = client.FromContext(ctx).API()
		io     = iostreams.FromContext(ctx)
	)

	status, err := client.GetAppStatus(ctx, app.Name, false)
	if err != nil {
		return fmt.Errorf("get app status: %w", err)
	}

	var vms []*api.AllocationStatus

	vms = append(vms, status.Allocations...)

	if len(vms) == 0 {
		return fmt.Errorf("no vms found")
	}

	agentclient, err := agent.Establish(ctx, client)
	if err != nil {
		return fmt.Errorf("can't establish agent %w", err)
	}

	dialer, err := agentclient.Dialer(ctx, app.Organization.Slug)
	if err != nil {
		return fmt.Errorf("can't build tunnel for %s: %s", app.Organization.Slug, err)
	}

	fmt.Fprintf(io.Out, "Restarting Postgres\n")

	for _, vm := range vms {
		fmt.Fprintf(io.Out, " Restarting %s\n", vm.ID)

		pgclient := flypg.NewFromInstance(fmt.Sprintf("[%s]", vm.PrivateIP), dialer)

		if err := pgclient.RestartNodePG(ctx); err != nil {
			return fmt.Errorf("failed to restart postgres on node: %w", err)
		}
	}
	fmt.Fprintf(io.Out, "Restart complete\n")

	return
}
