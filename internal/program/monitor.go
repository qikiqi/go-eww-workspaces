package program

import (
	"context"
	"fmt"

	sway "github.com/joshuarubin/go-sway"
)

// autoDetectSwayOutput returns the name of the first active sway output.
// Used when --output is not provided.
func autoDetectSwayOutput(ctx context.Context, client sway.Client) (string, error) {
	outputs, err := client.GetOutputs(ctx)
	if err != nil {
		return "", fmt.Errorf("get_outputs: %w", err)
	}
	for _, o := range outputs {
		if o.Active {
			return o.Name, nil
		}
	}
	return "", fmt.Errorf("no active monitor found")
}
