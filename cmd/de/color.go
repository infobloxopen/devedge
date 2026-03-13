package main

import (
	"os"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

func init() {
	if isCI() {
		color.NoColor = true
	}
}

// isCI returns true when running inside a known CI/CD system.
// fatih/color already disables colors when stdout is not a TTY or NO_COLOR/TERM=dumb
// is set; this adds explicit checks for CI environments that may have a pseudo-TTY.
func isCI() bool {
	ciVars := []string{
		"CI",                     // GitHub Actions, Travis, CircleCI, and most others
		"GITHUB_ACTIONS",
		"JENKINS_HOME",
		"JENKINS_URL",
		"GITLAB_CI",
		"TF_BUILD",               // Azure DevOps
		"CIRCLECI",
		"TRAVIS",
		"BITBUCKET_BUILD_NUMBER",
		"BUILDKITE",
		"TEAMCITY_VERSION",
	}
	for _, v := range ciVars {
		if os.Getenv(v) != "" {
			return true
		}
	}
	return false
}

var (
	colorSuccess = color.New(color.FgGreen, color.Bold)
	colorError   = color.New(color.FgRed, color.Bold)
	colorWarning = color.New(color.FgYellow)
	colorHeader  = color.New(color.Bold)
	colorHost    = color.New(color.FgCyan)
	colorLabel   = color.New(color.Faint)
)

// applyColoredHelp overrides Cobra's help template to add colors when the
// terminal supports them. No-ops when color.NoColor is true.
func applyColoredHelp(root *cobra.Command) {
	if color.NoColor {
		return
	}

	bold := "\033[1m"
	cyan := "\033[36m"
	faint := "\033[2m"
	rst := "\033[0m"

	// Based on Cobra's default template, extended with:
	//   - Short description shown as header when Long is absent
	//   - Bold section labels
	//   - Cyan command names
	//   - Faint footer hint
	tmpl := `{{with .Long}}` + bold + `{{. | trimRightSpace}}` + rst + `

{{else}}{{with .Short}}` + bold + `{{.}}` + rst + `

{{end}}{{end}}` + bold + `Usage:` + rst + `{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

` + bold + `Aliases:` + rst + `
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

` + bold + `Examples:` + rst + `
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}

` + bold + `Available Commands:` + rst + `{{range .Commands}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  ` + cyan + `{{rpad .Name .NamePadding}}` + rst + `  {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

` + bold + `Flags:` + rst + `
{{.LocalFlags.FlagUsages | trimRightSpace}}{{end}}{{if .HasAvailableInheritedFlags}}

` + bold + `Global Flags:` + rst + `
{{.InheritedFlags.FlagUsages | trimRightSpace}}{{end}}{{if .HasHelpSubCommands}}

` + bold + `Additional help topics:` + rst + `{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .Name .NamePadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}

` + faint + `Use "{{.CommandPath}} [command] --help" for more information about a command.` + rst + `
{{end}}`

	root.SetHelpTemplate(tmpl)
}
