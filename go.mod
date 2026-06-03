module github.com/infobloxopen/devedge

go 1.25.5

require (
	github.com/fatih/color v1.18.0
	github.com/miekg/dns v1.1.72
	github.com/spf13/cobra v1.10.2
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/hashicorp/errwrap v1.0.0 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/lib/pq v1.10.9 // indirect
)

require (
	github.com/golang-migrate/migrate/v4 v4.17.1
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	golang.org/x/mod v0.33.0 // indirect
	golang.org/x/net v0.52.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/tools v0.42.0 // indirect
)

replace github.com/golang-migrate/migrate/v4 => github.com/infobloxopen/migrate/v4 v4.16.3-0.20260414025640-b28cb3bc8342
