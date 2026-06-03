// Command migratesvc is a minimal test service image for the deploy-mode schema-migration
// e2es (feature 006). It satisfies the service-image contract (C2):
//
//	migratesvc migrate    -> converge the database at $DATABASE_URL to the bundled
//	                         /migrations target (up OR down) via the devedge applier,
//	                         persisting down steps to $DEVEDGE_DOWNSTORE. This is what the
//	                         pre-install/pre-upgrade hook Job runs.
//	migratesvc (no args)  -> run a trivial HTTP server so the workload Deployment rolls and
//	                         serves. (The e2e verifies the hook-applied schema directly
//	                         against the database, so the fixture needs no DB client.)
//
// It is a test fixture (under testdata, excluded from `go build ./...`); the e2e builds it
// for the cluster's arch, bakes a /migrations dir per image, and imports it into k3d.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/infobloxopen/devedge/internal/migrate"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		runMigrate()
		return
	}
	http.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	log.Fatal(http.ListenAndServe(":8080", nil))
}

// runMigrate converges the database to the image's bundled schema version (the contract
// the deploy hook Job invokes), reusing the same applier the daemon uses for local-run.
func runMigrate() {
	res, err := migrate.NewForkApplier().Migrate(context.Background(),
		os.Getenv("DATABASE_URL"),
		migrate.Source{Path: "/migrations"},
		migrate.DownStore{Dir: os.Getenv("DEVEDGE_DOWNSTORE")})
	if err != nil {
		log.Fatalf("migrate: %v", err)
	}
	log.Printf("migrate: v%d -> v%d (applied %d, alreadyCurrent %v)",
		res.FromVersion, res.ToVersion, res.Applied, res.AlreadyCurrent)
}
