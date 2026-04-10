package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/hermes-agent/hermes-agent-go/internal/config"
)

// RunProfileList prints all available profiles with their status.
func RunProfileList() {
	profiles := config.ListProfiles()
	active := config.GetActiveProfile()
	if active == "" {
		active = "default"
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tPATH\tSTATUS")

	for _, p := range profiles {
		status := ""
		if p.Name == active {
			status = "active"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", p.Name, p.Home, status)
	}
	w.Flush()
}

// RunProfileShow prints details about the currently active profile.
func RunProfileShow() {
	active := config.GetActiveProfile()
	if active == "" {
		active = "default"
	}
	home := config.GetProfileHome(active)

	fmt.Printf("Active profile: %s\n", active)
	fmt.Printf("Home directory: %s\n", home)
}

// RunProfileCreate creates a new named profile.
func RunProfileCreate(name string) error {
	if err := config.CreateProfile(name); err != nil {
		return fmt.Errorf("create profile: %w", err)
	}
	fmt.Printf("Profile %q created at %s\n", name, config.GetProfileHome(name))
	return nil
}

// RunProfileDelete deletes a named profile.
func RunProfileDelete(name string) error {
	if err := config.DeleteProfile(name); err != nil {
		return fmt.Errorf("delete profile: %w", err)
	}
	fmt.Printf("Profile %q deleted.\n", name)
	return nil
}

// RunProfileSwitch switches the active profile.
func RunProfileSwitch(name string) error {
	if err := config.SetActiveProfile(name); err != nil {
		return fmt.Errorf("switch profile: %w", err)
	}
	if name == "" || name == "default" {
		fmt.Println("Switched to default profile.")
	} else {
		fmt.Printf("Switched to profile %q.\n", name)
	}
	return nil
}
