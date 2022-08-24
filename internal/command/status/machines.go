package status

import (
	"context"

	"github.com/superfly/flyctl/api"
	"github.com/superfly/flyctl/flaps"
	"github.com/superfly/flyctl/internal/render"
	"github.com/superfly/flyctl/iostreams"
)

func renderMachineStatus(ctx context.Context, app *api.AppCompact) (err error) {
	io := iostreams.FromContext(ctx)
	flapsClient := flaps.FromContext(ctx)

	if err = flapsClient.EstablishForApp(ctx, app); err != nil {
		return err
	}

	machines, err := flapsClient.ListActive(ctx)
	if err != nil {
		return err
	}

	obj := [][]string{
		{
			app.Name,
			app.Organization.Slug,
			app.Hostname,
			app.PlatformVersion,
		},
	}

	if err = render.VerticalTable(io.Out, "App", obj, "Name", "Owner", "Hostname", "Platform"); err != nil {
		return
	}

	rows := [][]string{}

	for _, machine := range machines {
		rows = append(rows, []string{
			machine.ID,
			machine.State,
			machine.Region,
			machine.FullImageRef(),
			machine.CreatedAt,
			machine.UpdatedAt,
		})
	}

	_ = render.Table(io.Out, "", rows, "ID", "State", "Region", "Image", "Created", "Updated")

	return
}
