package config

import "testing"

func TestDependency_DefaultedPort(t *testing.T) {
	tests := []struct {
		name string
		dep  Dependency
		want int
	}{
		{"explicit port wins", Dependency{Engine: "postgres", Port: 6000}, 6000},
		{"postgres default", Dependency{Engine: "postgres"}, 5432},
		{"redis default", Dependency{Engine: "redis"}, 6379},
		{"unknown engine, no port", Dependency{Engine: "mysql"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.dep.DefaultedPort(); got != tt.want {
				t.Errorf("DefaultedPort() = %d, want %d", got, tt.want)
			}
		})
	}
}
