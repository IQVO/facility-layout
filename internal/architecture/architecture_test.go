// Package architecture holds fitness tests (the Go analogue of ArchUnit,
// via github.com/arch-go/arch-go) that enforce the hexagonal/ports-and-adapters
// dependency rule described in this project's CLAUDE.md: dependencies point
// inward only, and inbound/outbound adapters never depend on each other.
package architecture

import (
	"testing"

	archgo "github.com/arch-go/arch-go/api"
	"github.com/arch-go/arch-go/api/configuration"
)

const modulePath = "github.com/claudioed/facility-layout"

func TestHexagonalArchitecture(t *testing.T) {
	moduleInfo := configuration.Load(modulePath)

	// arch-go's package-pattern DSL uses '.' as the path-segment separator
	// (mirroring Java package notation), not '/': "**.internal.domain.**"
	// matches any Go import path containing an internal/domain segment,
	// e.g. github.com/claudioed/facility-layout/internal/domain/slot.
	// See arch-go's own arch-go.yml, which uses the identical convention
	// ("**.internal.**", "**.arch-go.cmd.**").

	t.Run("domain depends on nothing internal except domain", func(t *testing.T) {
		// The core rule. The domain is pure business logic with zero
		// knowledge of how it is invoked or persisted: no application
		// layer, no adapters, no composition root.
		rule := &configuration.DependenciesRule{
			Package: "**.internal.domain.**",
			ShouldOnlyDependsOn: &configuration.Dependencies{
				Internal: []string{"**.internal.domain.**"},
			},
		}

		result := archgo.CheckArchitecture(moduleInfo, configuration.Config{
			DependenciesRules: []*configuration.DependenciesRule{rule},
		})

		assertPass(t, result)
	})

	t.Run("application depends only on domain", func(t *testing.T) {
		// The application layer declares ports (interfaces) that adapters
		// implement; it never imports a concrete adapter.
		rule := &configuration.DependenciesRule{
			Package: "**.internal.application.**",
			ShouldOnlyDependsOn: &configuration.Dependencies{
				Internal: []string{
					"**.internal.domain.**",
					"**.internal.application.**",
				},
			},
		}

		result := archgo.CheckArchitecture(moduleInfo, configuration.Config{
			DependenciesRules: []*configuration.DependenciesRule{rule},
		})

		assertPass(t, result)
	})

	t.Run("inbound adapters do not depend on outbound adapters", func(t *testing.T) {
		// Inbound and outbound adapters are siblings that only communicate
		// through the application layer's ports — never directly.
		rule := &configuration.DependenciesRule{
			Package: "**.internal.adapters.inbound.**",
			ShouldNotDependsOn: &configuration.Dependencies{
				Internal: []string{"**.internal.adapters.outbound.**"},
			},
		}

		result := archgo.CheckArchitecture(moduleInfo, configuration.Config{
			DependenciesRules: []*configuration.DependenciesRule{rule},
		})

		assertPass(t, result)
	})

	t.Run("outbound adapters do not depend on inbound adapters", func(t *testing.T) {
		rule := &configuration.DependenciesRule{
			Package: "**.internal.adapters.outbound.**",
			ShouldNotDependsOn: &configuration.Dependencies{
				Internal: []string{"**.internal.adapters.inbound.**"},
			},
		}

		result := archgo.CheckArchitecture(moduleInfo, configuration.Config{
			DependenciesRules: []*configuration.DependenciesRule{rule},
		})

		assertPass(t, result)
	})

	t.Run("only cmd is the composition root wiring every layer", func(t *testing.T) {
		// Nothing under internal/** may import cmd/**: if it did, cmd would
		// no longer be a leaf composition root but a dependency of the very
		// layers it is supposed to wire together.
		rule := &configuration.DependenciesRule{
			Package: "**.internal.**",
			ShouldNotDependsOn: &configuration.Dependencies{
				Internal: []string{"**.cmd.**"},
			},
		}

		result := archgo.CheckArchitecture(moduleInfo, configuration.Config{
			DependenciesRules: []*configuration.DependenciesRule{rule},
		})

		assertPass(t, result)
	})

	t.Run("OLTP domain and application do not import the analytics read model", func(t *testing.T) {
		// The analytical data product (ADR-0010) is an additive read side. Its
		// read-model region (internal/analytics/report) and its store adapter
		// (internal/adapters/outbound/analyticsstore) must never be imported by
		// the OLTP domain or application layers: the report is derived FROM the
		// event stream, never dual-written from the transactional core. This is
		// the isolation that lets the analytics store move to a physically
		// separate database without touching OLTP code.
		rule := &configuration.DependenciesRule{
			Package: "**.internal.domain.**",
			ShouldNotDependsOn: &configuration.Dependencies{
				Internal: []string{
					"**.internal.analytics.**",
					"**.internal.adapters.outbound.analyticsstore.**",
				},
			},
		}

		result := archgo.CheckArchitecture(moduleInfo, configuration.Config{
			DependenciesRules: []*configuration.DependenciesRule{rule},
		})

		assertPass(t, result)
	})

	t.Run("the application layer does not import the analytics read model or store", func(t *testing.T) {
		rule := &configuration.DependenciesRule{
			Package: "**.internal.application.**",
			ShouldNotDependsOn: &configuration.Dependencies{
				Internal: []string{
					"**.internal.analytics.**",
					"**.internal.adapters.outbound.analyticsstore.**",
				},
			},
		}

		result := archgo.CheckArchitecture(moduleInfo, configuration.Config{
			DependenciesRules: []*configuration.DependenciesRule{rule},
		})

		assertPass(t, result)
	})

	t.Run("the analytics read model depends on nothing internal", func(t *testing.T) {
		// internal/analytics/report is a self-contained read-model region: it
		// declares the report shapes and the writer/reader ports and must import
		// no other internal package (not the OLTP domain, not an adapter), so it
		// can never drag transactional code into the projection.
		rule := &configuration.DependenciesRule{
			Package: "**.internal.analytics.**",
			ShouldOnlyDependsOn: &configuration.Dependencies{
				Internal: []string{"**.internal.analytics.**"},
			},
		}

		result := archgo.CheckArchitecture(moduleInfo, configuration.Config{
			DependenciesRules: []*configuration.DependenciesRule{rule},
		})

		assertPass(t, result)
	})

	t.Run("ports package only contains interfaces", func(t *testing.T) {
		// internal/application/ports declares OUT ports for adapters to
		// implement; it must never define a concrete struct or function,
		// or an adapter type would leak into the application layer.
		rule := &configuration.ContentsRule{
			Package:                     "**.internal.application.ports.**",
			ShouldOnlyContainInterfaces: true,
		}

		result := archgo.CheckArchitecture(moduleInfo, configuration.Config{
			ContentRules: []*configuration.ContentsRule{rule},
		})

		assertPass(t, result)
	})

	t.Run("the domain has no catch-all utils or common package", func(t *testing.T) {
		// A convention this codebase genuinely follows, encoded so it
		// cannot silently drift: every package under internal/domain is
		// named for an aggregate or for the shared value objects. A
		// "utils"/"common"/"helpers" package is where domain logic goes to
		// stop being domain logic.
		rule := &configuration.DependenciesRule{
			Package: "**.internal.**",
			ShouldNotDependsOn: &configuration.Dependencies{
				Internal: []string{
					"**.internal.domain.utils.**",
					"**.internal.domain.util.**",
					"**.internal.domain.common.**",
					"**.internal.domain.helpers.**",
				},
			},
		}

		result := archgo.CheckArchitecture(moduleInfo, configuration.Config{
			DependenciesRules: []*configuration.DependenciesRule{rule},
		})

		assertPass(t, result)
	})
}

func assertPass(t *testing.T, result *archgo.Result) {
	t.Helper()

	if result.Pass {
		return
	}

	if result.DependenciesRuleResult != nil {
		for _, r := range result.DependenciesRuleResult.Results {
			if !r.Passes {
				t.Errorf("%s", archViolation("dependency", r.Description, r.Verifications))
			}
		}
	}

	if result.ContentsRuleResult != nil {
		for _, r := range result.ContentsRuleResult.Results {
			if !r.Passes {
				t.Errorf("%s", archViolation("contents", r.Description, r.Verifications))
			}
		}
	}

	t.FailNow()
}
