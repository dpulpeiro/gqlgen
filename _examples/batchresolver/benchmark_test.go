package batchresolver

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
)

const nestedCount = 10

func buildNestedData() (
	users []*User,
	profiles []*Profile,
	userProfiles map[*User][]*Profile,
) {
	users = make([]*User, nestedCount)
	userProfiles = make(map[*User][]*Profile, nestedCount)

	for u := range nestedCount {
		user := &User{}
		users[u] = user
		ps := make([]*Profile, nestedCount)
		for p := range nestedCount {
			profile := &Profile{ID: fmt.Sprintf("p%d_%d", u, p)}
			ps[p] = profile
			profiles = append(profiles, profile)
		}
		userProfiles[user] = ps
	}
	return
}

func BenchmarkNestedBatchResolver(b *testing.B) {
	users, profiles, userProfiles := buildNestedData()

	resolver := &Resolver{
		users:         users,
		profiles:      profiles,
		userProfiles:  userProfiles,
		profileErrIdx: -1,
	}

	srv := handler.New(NewExecutableSchema(Config{Resolvers: resolver}))
	srv.AddTransport(transport.POST{})

	batchQuery := `{"query":"{ users { profileBatch { viewerCanDeleteBatch } } }"}`
	nonBatchQuery := `{"query":"{ users { profileNonBatch { viewerCanDeleteNonBatch } } }"}`

	run := func(b *testing.B, name, query string) {
		b.Run(name, func(b *testing.B) {
			var body strings.Reader
			r := httptest.NewRequest(http.MethodPost, "/graphql", &body)
			r.Header.Set("Content-Type", "application/json")

			b.ReportAllocs()
			b.ResetTimer()

			rec := httptest.NewRecorder()
			for i := 0; i < b.N; i++ {
				resolver.resolverCalls.Store(0)
				body.Reset(query)
				rec.Body.Reset()
				srv.ServeHTTP(rec, r)
				if rec.Code != http.StatusOK {
					b.Fatalf("unexpected status %d: %s", rec.Code, rec.Body.String())
				}
			}

			calls := resolver.resolverCalls.Load()
			b.ReportMetric(float64(calls), "calls/op")
		})
	}

	run(b, "batch", batchQuery)
	run(b, "non_batch", nonBatchQuery)
}
