package cli

import (
	"fmt"
	"os"
	"text/tabwriter"
)

// RunThemeList prints all available themes.
func RunThemeList() {
	skins := ListSkins()
	activeName := GetActiveSkinName()

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tDESCRIPTION\tSTATUS")

	for _, s := range skins {
		status := ""
		if s["name"] == activeName {
			status = "active"
		}
		desc := s["description"]
		if desc == "" {
			desc = "(no description)"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", s["name"], desc, status)
	}
	w.Flush()
}

// RunThemeShow prints the currently active theme.
func RunThemeShow() {
	name := GetActiveSkinName()
	if name == "" {
		name = "default"
	}
	fmt.Printf("Active theme: %s\n", name)
}

// RunThemeSwitch switches the active theme.
func RunThemeSwitch(name string) error {
	// Validate the theme exists in known skins.
	found := false
	for _, s := range ListSkins() {
		if s["name"] == name {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("theme %q not found", name)
	}
	SetActiveSkin(name)
	fmt.Printf("Switched to theme %q.\n", name)
	return nil
}
