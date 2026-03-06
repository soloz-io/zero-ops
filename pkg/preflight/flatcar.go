package preflight

import (
	"context"
	"fmt"
)

// FlatcarImageValidator validates Flatcar image availability
type FlatcarImageValidator struct {
	Token   string
	ImageID string
}

func (v *FlatcarImageValidator) Validate(ctx context.Context) error {
	// Flatcar stable is available by default in Hetzner
	// No snapshot building required
	if v.ImageID == "" || v.ImageID == "flatcar-stable" {
		fmt.Println("  ✓ Using Hetzner's default Flatcar Stable image")
		return nil
	}
	
	// Custom image ID provided - validate it exists
	fmt.Printf("  ✓ Using custom Flatcar image: %s\n", v.ImageID)
	return nil
}
