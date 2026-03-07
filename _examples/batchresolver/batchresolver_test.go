package batchresolver

import (
	"encoding/json"
	"fmt"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/99designs/gqlgen/client"
	"github.com/99designs/gqlgen/codegen/config"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
)

type gqlError struct {
	Message string `json:"message"`
	Path    []any  `json:"path"`
}

func newTestClient(r *Resolver) *client.Client {
	srv := handler.New(NewExecutableSchema(Config{Resolvers: r}))
	srv.AddTransport(transport.POST{})
	return client.New(srv)
}

func marshalJSON(t *testing.T, v any) string {
	t.Helper()
	blob, err := json.Marshal(v)
	require.NoError(t, err)
	return string(blob)
}

func requireErrorJSON(t *testing.T, err error, expected string) {
	t.Helper()
	require.Error(t, err)
	actual := normalizeErrorJSON(t, err.Error())
	expectedNorm := normalizeErrorJSON(t, expected)
	require.Equal(t, expectedNorm, actual)
}

func normalizeErrorJSON(t *testing.T, jsonStr string) string {
	t.Helper()
	if jsonStr == "" {
		return ""
	}
	var list []gqlError
	require.NoError(t, json.Unmarshal([]byte(jsonStr), &list))
	sort.Slice(list, func(i, j int) bool {
		return errorKey(t, list[i]) < errorKey(t, list[j])
	})
	blob, err := json.Marshal(list)
	require.NoError(t, err)
	return string(blob)
}

func errorKey(t *testing.T, err gqlError) string {
	t.Helper()
	blob, marshalErr := json.Marshal(err.Path)
	require.NoError(t, marshalErr)
	return err.Message + "|" + string(blob)
}

func TestBatchResolver_Parity_NoError(t *testing.T) {
	resolver := &Resolver{
		users:         []*User{{}, {}},
		profiles:      []*Profile{{ID: "p1"}, {ID: "p2"}},
		profileErrIdx: -1,
	}

	c := newTestClient(resolver)
	var resp struct {
		Users []struct {
			NullableBatch *struct {
				ID string `json:"id"`
			} `json:"nullableBatch"`
			NullableNonBatch *struct {
				ID string `json:"id"`
			} `json:"nullableNonBatch"`
		} `json:"users"`
	}

	err := c.Post(`query { users { nullableBatch { id } nullableNonBatch { id } } }`, &resp)
	require.NoError(t, err)
	require.JSONEq(
		t,
		`{"users":[{"nullableBatch":{"id":"p1"},"nullableNonBatch":{"id":"p1"}},{"nullableBatch":{"id":"p2"},"nullableNonBatch":{"id":"p2"}}]}`,
		marshalJSON(t, resp),
	)
}

func TestBatchResolver_Parity_WithArgs(t *testing.T) {
	resolver := &Resolver{
		users:         []*User{{}, {}},
		profiles:      []*Profile{{ID: "p1"}, {ID: "p2"}, {ID: "p3"}},
		profileErrIdx: -1,
	}

	c := newTestClient(resolver)
	var resp struct {
		Users []struct {
			NullableBatchWithArg *struct {
				ID string `json:"id"`
			} `json:"nullableBatchWithArg"`
			NullableNonBatchWithArg *struct {
				ID string `json:"id"`
			} `json:"nullableNonBatchWithArg"`
		} `json:"users"`
	}

	err := c.Post(`
query {
  users {
    nullableBatchWithArg(offset: 1) { id }
    nullableNonBatchWithArg(offset: 1) { id }
  }
}`, &resp)
	require.NoError(t, err)
	require.JSONEq(
		t,
		`{"users":[{"nullableBatchWithArg":{"id":"p2"},"nullableNonBatchWithArg":{"id":"p2"}},{"nullableBatchWithArg":{"id":"p3"},"nullableNonBatchWithArg":{"id":"p3"}}]}`,
		marshalJSON(t, resp),
	)
}

func TestBatchResolver_Parity_Error(t *testing.T) {
	resolver := &Resolver{
		users:         []*User{{}, {}},
		profiles:      []*Profile{{ID: "p1"}, {ID: "p2"}},
		profileErrIdx: 1,
	}

	c := newTestClient(resolver)
	var resp struct {
		Users []struct {
			NullableBatch *struct {
				ID string `json:"id"`
			} `json:"nullableBatch"`
			NullableNonBatch *struct {
				ID string `json:"id"`
			} `json:"nullableNonBatch"`
		} `json:"users"`
	}

	err := c.Post(`query { users { nullableBatch { id } nullableNonBatch { id } } }`, &resp)
	requireErrorJSON(t, err, `[
		{"message":"profile error at index 1","path":["users",1,"nullableBatch"]},
		{"message":"profile error at index 1","path":["users",1,"nullableNonBatch"]}
	]`)
	require.JSONEq(
		t,
		`{"users":[{"nullableBatch":{"id":"p1"},"nullableNonBatch":{"id":"p1"}},{"nullableBatch":null,"nullableNonBatch":null}]}`,
		marshalJSON(t, resp),
	)
}

func TestBatchResolver_Parity_GqlErrorList(t *testing.T) {
	resolver := &Resolver{
		users:              []*User{{}, {}},
		profiles:           []*Profile{{ID: "p1"}, {ID: "p2"}},
		profileErrListIdxs: map[int]struct{}{0: {}},
		profileErrIdx:      -1,
	}

	c := newTestClient(resolver)
	var resp struct {
		Users []struct {
			NullableBatch *struct {
				ID string `json:"id"`
			} `json:"nullableBatch"`
			NullableNonBatch *struct {
				ID string `json:"id"`
			} `json:"nullableNonBatch"`
		} `json:"users"`
	}

	err := c.Post(`query { users { nullableBatch { id } nullableNonBatch { id } } }`, &resp)
	requireErrorJSON(t, err, `[
		{"message":"profile list error 1 at index 0","path":["users",0,"nullableBatch"]},
		{"message":"profile list error 2 at index 0","path":["users",0,"nullableBatch"]},
		{"message":"profile list error 1 at index 0","path":["users",0,"nullableNonBatch"]},
		{"message":"profile list error 2 at index 0","path":["users",0,"nullableNonBatch"]}
	]`)
	require.JSONEq(
		t,
		`{"users":[{"nullableBatch":null,"nullableNonBatch":null},{"nullableBatch":{"id":"p2"},"nullableNonBatch":{"id":"p2"}}]}`,
		marshalJSON(t, resp),
	)
}

func TestBatchResolver_Parity_GqlErrorPathNil(t *testing.T) {
	resolver := &Resolver{
		users:                   []*User{{}, {}},
		profiles:                []*Profile{{ID: "p1"}, {ID: "p2"}},
		profileGqlErrNoPathIdxs: map[int]struct{}{0: {}},
		profileErrIdx:           -1,
	}

	c := newTestClient(resolver)
	var resp struct {
		Users []struct {
			NullableBatch *struct {
				ID string `json:"id"`
			} `json:"nullableBatch"`
			NullableNonBatch *struct {
				ID string `json:"id"`
			} `json:"nullableNonBatch"`
		} `json:"users"`
	}

	err := c.Post(`query { users { nullableBatch { id } nullableNonBatch { id } } }`, &resp)
	requireErrorJSON(t, err, `[
		{"message":"profile gqlerror path nil at index 0","path":["users",0,"nullableBatch"]},
		{"message":"profile gqlerror path nil at index 0","path":["users",0,"nullableNonBatch"]}
	]`)
	require.JSONEq(
		t,
		`{"users":[{"nullableBatch":null,"nullableNonBatch":null},{"nullableBatch":{"id":"p2"},"nullableNonBatch":{"id":"p2"}}]}`,
		marshalJSON(t, resp),
	)
}

func TestBatchResolver_Parity_GqlErrorPathSet(t *testing.T) {
	resolver := &Resolver{
		users:                 []*User{{}, {}},
		profiles:              []*Profile{{ID: "p1"}, {ID: "p2"}},
		profileGqlErrPathIdxs: map[int]struct{}{0: {}},
		profileErrIdx:         -1,
	}

	c := newTestClient(resolver)
	var resp struct {
		Users []struct {
			NullableBatch *struct {
				ID string `json:"id"`
			} `json:"nullableBatch"`
			NullableNonBatch *struct {
				ID string `json:"id"`
			} `json:"nullableNonBatch"`
		} `json:"users"`
	}

	err := c.Post(`query { users { nullableBatch { id } nullableNonBatch { id } } }`, &resp)
	requireErrorJSON(t, err, `[
		{"message":"profile gqlerror path set at index 0","path":["custom",0]},
		{"message":"profile gqlerror path set at index 0","path":["custom",0]}
	]`)
	require.JSONEq(
		t,
		`{"users":[{"nullableBatch":null,"nullableNonBatch":null},{"nullableBatch":{"id":"p2"},"nullableNonBatch":{"id":"p2"}}]}`,
		marshalJSON(t, resp),
	)
}

func TestBatchResolver_Parity_PartialResponseWithErrValue(t *testing.T) {
	resolver := &Resolver{
		users:                   []*User{{}, {}},
		profiles:                []*Profile{{ID: "p1"}, {ID: "p2"}},
		profileErrWithValueIdxs: map[int]struct{}{0: {}},
		profileErrIdx:           -1,
	}

	c := newTestClient(resolver)
	var resp struct {
		Users []struct {
			NullableBatch *struct {
				ID string `json:"id"`
			} `json:"nullableBatch"`
			NullableNonBatch *struct {
				ID string `json:"id"`
			} `json:"nullableNonBatch"`
		} `json:"users"`
	}

	err := c.Post(`query { users { nullableBatch { id } nullableNonBatch { id } } }`, &resp)
	requireErrorJSON(t, err, `[
		{"message":"profile error with value at index 0","path":["users",0,"nullableBatch"]},
		{"message":"profile error with value at index 0","path":["users",0,"nullableNonBatch"]}
	]`)
	require.JSONEq(
		t,
		`{"users":[{"nullableBatch":null,"nullableNonBatch":null},{"nullableBatch":{"id":"p2"},"nullableNonBatch":{"id":"p2"}}]}`,
		marshalJSON(t, resp),
	)
}

func TestBatchResolver_Parity_NonNullPropagation(t *testing.T) {
	resolver := &Resolver{
		users:         []*User{{}, {}},
		profiles:      []*Profile{{ID: "p1"}, {ID: "p2"}},
		profileErrIdx: 0,
	}

	c := newTestClient(resolver)
	var resp struct {
		Users []struct {
			NonNullableBatch *struct {
				ID string `json:"id"`
			} `json:"nonNullableBatch"`
			NonNullableNonBatch *struct {
				ID string `json:"id"`
			} `json:"nonNullableNonBatch"`
		} `json:"users"`
	}

	err := c.Post(`query { users { nonNullableBatch { id } nonNullableNonBatch { id } } }`, &resp)
	requireErrorJSON(t, err, `[
		{"message":"profile error at index 0","path":["users",0,"nonNullableBatch"]},
		{"message":"profile error at index 0","path":["users",0,"nonNullableNonBatch"]}
	]`)
	require.JSONEq(
		t,
		`{"users":null}`,
		marshalJSON(t, resp),
	)
}

func TestBatchResolver_InvalidLen_AddsErrorPerParent(t *testing.T) {
	resolver := &Resolver{
		users:           []*User{{}, {}},
		profiles:        []*Profile{{ID: "p1"}, {ID: "p2"}},
		profileErrIdx:   -1,
		profileWrongLen: true,
	}

	c := newTestClient(resolver)
	var resp struct {
		Users []struct {
			NullableBatch *struct {
				ID string `json:"id"`
			} `json:"nullableBatch"`
		} `json:"users"`
	}

	err := c.Post(`query { users { nullableBatch { id } } }`, &resp)
	requireErrorJSON(t, err, `[
		{"message":"index 0: batch resolver User.nullableBatch returned 1 results for 2 parents","path":["users",0,"nullableBatch"]},
		{"message":"index 1: batch resolver User.nullableBatch returned 1 results for 2 parents","path":["users",1,"nullableBatch"]}
	]`)
	require.JSONEq(
		t,
		`{"users":[{"nullableBatch":null},{"nullableBatch":null}]}`,
		marshalJSON(t, resp),
	)
}

func TestBatchResolver_BatchErrors_ErrLenMismatch_AddsErrorPerParent(t *testing.T) {
	cases := []struct {
		name   string
		errLen int
	}{
		{name: "len1", errLen: 1},
		{name: "len0", errLen: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolver := &Resolver{
				users:             []*User{{}, {}},
				profiles:          []*Profile{{ID: "p1"}, {ID: "p2"}},
				profileErrIdx:     -1,
				batchErrsWrongLen: true,
				batchErrsLen:      tc.errLen,
			}

			c := newTestClient(resolver)
			var resp struct {
				Users []struct {
					NullableBatch *struct {
						ID string `json:"id"`
					} `json:"nullableBatch"`
				} `json:"users"`
			}

			err := c.Post(`query { users { nullableBatch { id } } }`, &resp)
			requireErrorJSON(t, err, fmt.Sprintf(`[
				{"message":"index 0: batch resolver User.nullableBatch returned %d errors for 2 parents","path":["users",0,"nullableBatch"]},
				{"message":"index 1: batch resolver User.nullableBatch returned %d errors for 2 parents","path":["users",1,"nullableBatch"]}
			]`, tc.errLen, tc.errLen))
			require.JSONEq(
				t,
				`{"users":[{"nullableBatch":null},{"nullableBatch":null}]}`,
				marshalJSON(t, resp),
			)
		})
	}
}

func TestBatchResolver_BatchErrors_ResultLenMismatch_AddsErrorPerParent(t *testing.T) {
	resolver := &Resolver{
		users:                []*User{{}, {}},
		profiles:             []*Profile{{ID: "p1"}, {ID: "p2"}},
		profileErrIdx:        -1,
		batchResultsWrongLen: true,
		batchResultsLen:      1,
	}

	c := newTestClient(resolver)
	var resp struct {
		Users []struct {
			NullableBatch *struct {
				ID string `json:"id"`
			} `json:"nullableBatch"`
		} `json:"users"`
	}

	err := c.Post(`query { users { nullableBatch { id } } }`, &resp)
	requireErrorJSON(t, err, `[
		{"message":"index 0: batch resolver User.nullableBatch returned 1 results for 2 parents","path":["users",0,"nullableBatch"]},
		{"message":"index 1: batch resolver User.nullableBatch returned 1 results for 2 parents","path":["users",1,"nullableBatch"]}
	]`)
	require.JSONEq(
		t,
		`{"users":[{"nullableBatch":null},{"nullableBatch":null}]}`,
		marshalJSON(t, resp),
	)
}

func TestBatchDirectiveConfig(t *testing.T) {
	cfg, err := config.LoadConfig("gqlgen.yml")
	require.NoError(t, err)
	require.NoError(t, cfg.Init())

	userFields := cfg.Models["User"].Fields

	// YAML-configured fields
	require.True(t, userFields["nullableBatch"].Batch)
	require.True(t, userFields["nullableBatchWithArg"].Batch)
	require.True(t, userFields["nonNullableBatch"].Batch)

	require.False(t, userFields["nullableNonBatch"].Batch)
	require.False(t, userFields["nullableNonBatchWithArg"].Batch)
	require.False(t, userFields["nonNullableNonBatch"].Batch)

	// Directive-configured fields
	require.True(t, userFields["directiveNullableBatch"].Batch)
	require.True(t, userFields["directiveNullableBatchWithArg"].Batch)
	require.True(t, userFields["directiveNonNullableBatch"].Batch)

	require.False(t, userFields["directiveNullableNonBatch"].Batch)
	require.False(t, userFields["directiveNullableNonBatchWithArg"].Batch)
	require.False(t, userFields["directiveNonNullableNonBatch"].Batch)
}

func TestBatchResolver_BatchErrors_ListPerIndex_AddsMultipleErrors(t *testing.T) {
	resolver := &Resolver{
		users:            []*User{{}, {}},
		profiles:         []*Profile{{ID: "p1"}, {ID: "p2"}},
		profileErrIdx:    -1,
		batchErrListIdxs: map[int]struct{}{0: {}},
	}

	c := newTestClient(resolver)
	var resp struct {
		Users []struct {
			NullableBatch *struct {
				ID string `json:"id"`
			} `json:"nullableBatch"`
		} `json:"users"`
	}

	err := c.Post(`query { users { nullableBatch { id } } }`, &resp)
	requireErrorJSON(t, err, `[
		{"message":"batch list error 1 at index 0","path":["users",0,"nullableBatch"]},
		{"message":"batch list error 2 at index 0","path":["users",0,"nullableBatch"]}
	]`)
	require.JSONEq(
		t,
		`{"users":[{"nullableBatch":null},{"nullableBatch":{"id":"p2"}}]}`,
		marshalJSON(t, resp),
	)
}

func TestBatchResolver_Nested_CallCount(t *testing.T) {
	const n = 10
	users := make([]*User, n)
	profiles := make([]*Profile, n)
	images := make([]*Image, n)
	for i := 0; i < n; i++ {
		users[i] = &User{}
		profiles[i] = &Profile{ID: fmt.Sprintf("p%d", i)}
		images[i] = &Image{URL: fmt.Sprintf("https://img/%d", i)}
	}
	resolver := &Resolver{
		users:         users,
		profiles:      profiles,
		images:        images,
		profileErrIdx: -1,
	}
	client := newTestClient(resolver)

	type graphqlResp struct {
		Users []struct {
			Profile *struct {
				ID    string `json:"id"`
				Cover *struct {
					URL string `json:"url"`
				} `json:"cover"`
			} `json:"profile"`
		} `json:"users"`
	}

	assertData := func(t *testing.T, resp graphqlResp, label string) {
		t.Helper()
		require.Len(t, resp.Users, n)
		for i, u := range resp.Users {
			require.NotNil(t, u.Profile, "%s user %d profile nil", label, i)
			require.Equal(t, fmt.Sprintf("p%d", i), u.Profile.ID)
			require.NotNil(t, u.Profile.Cover, "%s user %d cover nil", label, i)
			require.Equal(t, fmt.Sprintf("https://img/%d", i), u.Profile.Cover.URL)
		}
	}

	// --- Batch path ---

	var batchResp graphqlResp
	err := client.Post(`query {
		users {
			profile: profileBatch {
				id
				cover: coverBatch {
					url
				}
			}
		}
	}`, &batchResp)
	require.NoError(t, err)
	assertData(t, batchResp, "batch")
	require.Equal(
		t,
		int32(1),
		resolver.profileBatchCalls.Load(),
		"profileBatch should be called once for all users",
	)
	require.Equal(
		t,
		int32(1),
		resolver.coverBatchCalls.Load(),
		"coverBatch should be called once for all profiles (nested batch propagation)",
	)

	// --- Non-batch path ---
	var nonBatchResp graphqlResp
	err = client.Post(`query {
		users {
			profile: profileNonBatch {
				id
				cover: coverNonBatch {
					url
				}
			}
		}
	}`, &nonBatchResp)
	require.NoError(t, err)
	assertData(t, nonBatchResp, "non-batch")
	require.Equal(
		t,
		int32(n),
		resolver.profileNonBatchCalls.Load(),
		"profileNonBatch should be called once per user",
	)
	require.Equal(
		t,
		int32(n),
		resolver.coverNonBatchCalls.Load(),
		"coverNonBatch should be called once per profile",
	)

	// --- Verify both paths produce identical data ---
	require.Equal(
		t,
		marshalJSON(t, batchResp),
		marshalJSON(t, nonBatchResp),
		"batch and non-batch should return identical data",
	)
}

func TestBatchResolver_Nested_Connection_CallCount(t *testing.T) {
	const n = 10
	users := make([]*User, n)
	profiles := make([]*Profile, n)
	images := make([]*Image, n)
	for i := 0; i < n; i++ {
		users[i] = &User{}
		profiles[i] = &Profile{ID: fmt.Sprintf("p%d", i)}
		images[i] = &Image{URL: fmt.Sprintf("https://img/%d", i)}
	}
	resolver := &Resolver{
		users:         users,
		profiles:      profiles,
		images:        images,
		profileErrIdx: -1,
	}
	client := newTestClient(resolver)

	type graphqlResp struct {
		Users []struct {
			Conn *struct {
				Edges []struct {
					Node *struct {
						ID    string `json:"id"`
						Cover *struct {
							URL string `json:"url"`
						} `json:"cover"`
					} `json:"node"`
				} `json:"edges"`
			} `json:"conn"`
		} `json:"users"`
	}

	assertData := func(t *testing.T, resp graphqlResp, label string) {
		t.Helper()
		require.Len(t, resp.Users, n)
		for i, u := range resp.Users {
			require.NotNil(t, u.Conn, "%s user %d connection nil", label, i)
			require.Len(t, u.Conn.Edges, 1, "%s user %d edges", label, i)
			node := u.Conn.Edges[0].Node
			require.NotNil(t, node, "%s user %d node nil", label, i)
			require.Equal(t, fmt.Sprintf("p%d", i), node.ID)
			require.NotNil(t, node.Cover, "%s user %d cover nil", label, i)
			require.Equal(t, fmt.Sprintf("https://img/%d", i), node.Cover.URL)
		}
	}

	// --- Batch path ---

	var batchResp graphqlResp
	err := client.Post(`query {
		users {
			conn: profileConnectionBatch {
				edges {
					node {
						id
						cover: coverBatch { url }
					}
				}
			}
		}
	}`, &batchResp)
	require.NoError(t, err)
	assertData(t, batchResp, "batch")
	require.Equal(
		t,
		int32(1),
		resolver.profileConnectionBatchCalls.Load(),
		"profileConnectionBatch should be called once for all users",
	)
	require.Equal(
		t,
		int32(1),
		resolver.coverBatchCalls.Load(),
		"coverBatch should be called once for all profiles (nested batch propagation through connections)",
	)

	// --- Non-batch path ---

	var nonBatchResp graphqlResp
	err = client.Post(`query {
		users {
			conn: profileConnectionNonBatch {
				edges {
					node {
						id
						cover: coverNonBatch { url }
					}
				}
			}
		}
	}`, &nonBatchResp)
	require.NoError(t, err)
	assertData(t, nonBatchResp, "non-batch")
	require.Equal(
		t,
		int32(n),
		resolver.profileConnectionNonBatchCalls.Load(),
		"profileConnectionNonBatch should be called once per user",
	)
	require.Equal(
		t,
		int32(n),
		resolver.coverNonBatchCalls.Load(),
		"coverNonBatch should be called once per profile",
	)

	// --- Verify both paths produce identical data ---
	require.Equal(
		t,
		marshalJSON(t, batchResp),
		marshalJSON(t, nonBatchResp),
		"batch and non-batch should return identical data",
	)
}

// TestBatchResolver_Nested_ExpectedBatching verifies the expected call counts
// for nested batch resolvers once batch context propagation is implemented.
// These tests currently FAIL because nested batching does not propagate batch
// parent context from batch resolver results to child resolvers.
// See nested-batch-propagation.md for the full problem description.
func TestBatchResolver_Nested_ExpectedBatching(t *testing.T) {
	const n = 10

	setup := func() (*Resolver, *client.Client) {
		users := make([]*User, n)
		profiles := make([]*Profile, n)
		images := make([]*Image, n)
		for i := 0; i < n; i++ {
			users[i] = &User{}
			profiles[i] = &Profile{ID: fmt.Sprintf("p%d", i)}
			images[i] = &Image{URL: fmt.Sprintf("https://img/%d", i)}
		}
		r := &Resolver{
			users:         users,
			profiles:      profiles,
			images:        images,
			profileErrIdx: -1,
		}
		return r, newTestClient(r)
	}

	// Query 1: users → profileBatch → coverBatch
	// Expected: 1 call per batch level.
	t.Run("SimpleNestedBatch", func(t *testing.T) {
		resolver, c := setup()

		var resp struct {
			Users []struct {
				ProfileBatch *struct {
					ID         string `json:"id"`
					CoverBatch *struct {
						URL string `json:"url"`
					} `json:"coverBatch"`
				} `json:"profileBatch"`
			} `json:"users"`
		}

		err := c.Post(`query {
			users {
				profileBatch {
					id
					coverBatch { url }
				}
			}
		}`, &resp)
		require.NoError(t, err)
		require.Len(t, resp.Users, n)
		for i, u := range resp.Users {
			require.NotNil(t, u.ProfileBatch, "user %d profile nil", i)
			require.Equal(t, fmt.Sprintf("p%d", i), u.ProfileBatch.ID)
			require.NotNil(t, u.ProfileBatch.CoverBatch, "user %d cover nil", i)
			require.Equal(t, fmt.Sprintf("https://img/%d", i), u.ProfileBatch.CoverBatch.URL)
		}

		require.Equal(t, int32(1), resolver.profileBatchCalls.Load(),
			"profileBatch should be called once for all users")
		require.Equal(t, int32(1), resolver.coverBatchCalls.Load(),
			"coverBatch should be called once for all profiles")
	})

	// Query 2: usersA/usersB aliases → profileBatch → coverBatch
	// Expected: each root alias is an independent batch scope.
	t.Run("AliasedRoots", func(t *testing.T) {
		resolver, c := setup()

		type userWithProfile struct {
			ProfileBatch *struct {
				ID         string `json:"id"`
				CoverBatch *struct {
					URL string `json:"url"`
				} `json:"coverBatch"`
			} `json:"profileBatch"`
		}
		var resp struct {
			UsersA []userWithProfile `json:"usersA"`
			UsersB []userWithProfile `json:"usersB"`
		}

		err := c.Post(`query {
			usersA: users {
				profileBatch {
					id
					coverBatch { url }
				}
			}
			usersB: users {
				profileBatch {
					id
					coverBatch { url }
				}
			}
		}`, &resp)
		require.NoError(t, err)
		require.Len(t, resp.UsersA, n)
		require.Len(t, resp.UsersB, n)
		for i := 0; i < n; i++ {
			require.NotNil(t, resp.UsersA[i].ProfileBatch, "usersA[%d] profile nil", i)
			require.Equal(t, fmt.Sprintf("p%d", i), resp.UsersA[i].ProfileBatch.ID)
			require.NotNil(t, resp.UsersA[i].ProfileBatch.CoverBatch, "usersA[%d] cover nil", i)
			require.Equal(t, fmt.Sprintf("https://img/%d", i), resp.UsersA[i].ProfileBatch.CoverBatch.URL)

			require.NotNil(t, resp.UsersB[i].ProfileBatch, "usersB[%d] profile nil", i)
			require.Equal(t, fmt.Sprintf("p%d", i), resp.UsersB[i].ProfileBatch.ID)
			require.NotNil(t, resp.UsersB[i].ProfileBatch.CoverBatch, "usersB[%d] cover nil", i)
			require.Equal(t, fmt.Sprintf("https://img/%d", i), resp.UsersB[i].ProfileBatch.CoverBatch.URL)
		}

		require.Equal(t, int32(2), resolver.profileBatchCalls.Load(),
			"profileBatch should be called once per root alias")
		require.Equal(t, int32(2), resolver.coverBatchCalls.Load(),
			"coverBatch should be called once per root alias's profiles")
	})

	// Query 3: users → profileConnectionBatch → edges → node → coverBatch
	// Expected: batch context propagates through non-batched intermediate types.
	t.Run("ConnectionNestedBatch", func(t *testing.T) {
		resolver, c := setup()

		var resp struct {
			Users []struct {
				Conn *struct {
					Edges []struct {
						Node *struct {
							ID         string `json:"id"`
							CoverBatch *struct {
								URL string `json:"url"`
							} `json:"coverBatch"`
						} `json:"node"`
					} `json:"edges"`
				} `json:"conn"`
			} `json:"users"`
		}

		err := c.Post(`query {
			users {
				conn: profileConnectionBatch {
					edges {
						node {
							id
							coverBatch { url }
						}
					}
				}
			}
		}`, &resp)
		require.NoError(t, err)
		require.Len(t, resp.Users, n)
		for i, u := range resp.Users {
			require.NotNil(t, u.Conn, "user %d connection nil", i)
			require.Len(t, u.Conn.Edges, 1, "user %d edges", i)
			node := u.Conn.Edges[0].Node
			require.NotNil(t, node, "user %d node nil", i)
			require.Equal(t, fmt.Sprintf("p%d", i), node.ID)
			require.NotNil(t, node.CoverBatch, "user %d cover nil", i)
			require.Equal(t, fmt.Sprintf("https://img/%d", i), node.CoverBatch.URL)
		}

		require.Equal(t, int32(1), resolver.profileConnectionBatchCalls.Load(),
			"profileConnectionBatch should be called once for all users")
		require.Equal(t, int32(1), resolver.coverBatchCalls.Load(),
			"coverBatch should be called once for all profiles across connection edges")
	})

	// Query 4: aliased roots × aliased connection fields → coverBatch
	// Expected: each aliased field creates its own independent batch scope.
	t.Run("AliasedRootsAndConnections", func(t *testing.T) {
		resolver, c := setup()

		type connResp struct {
			Edges []struct {
				Node *struct {
					ID         string `json:"id"`
					CoverBatch *struct {
						URL string `json:"url"`
					} `json:"coverBatch"`
				} `json:"node"`
			} `json:"edges"`
		}
		var resp struct {
			UsersA []struct {
				ConnA *connResp `json:"connA"`
				ConnB *connResp `json:"connB"`
			} `json:"usersA"`
			UsersB []struct {
				ConnC *connResp `json:"connC"`
				ConnD *connResp `json:"connD"`
			} `json:"usersB"`
		}

		err := c.Post(`query {
			usersA: users {
				connA: profileConnectionBatch {
					edges { node { id coverBatch { url } } }
				}
				connB: profileConnectionBatch {
					edges { node { id coverBatch { url } } }
				}
			}
			usersB: users {
				connC: profileConnectionBatch {
					edges { node { id coverBatch { url } } }
				}
				connD: profileConnectionBatch {
					edges { node { id coverBatch { url } } }
				}
			}
		}`, &resp)
		require.NoError(t, err)

		assertConn := func(conn *connResp, label string, i int) {
			t.Helper()
			require.NotNil(t, conn, "%s user %d connection nil", label, i)
			require.Len(t, conn.Edges, 1, "%s user %d edges", label, i)
			node := conn.Edges[0].Node
			require.NotNil(t, node, "%s user %d node nil", label, i)
			require.Equal(t, fmt.Sprintf("p%d", i), node.ID)
			require.NotNil(t, node.CoverBatch, "%s user %d cover nil", label, i)
			require.Equal(t, fmt.Sprintf("https://img/%d", i), node.CoverBatch.URL)
		}

		require.Len(t, resp.UsersA, n)
		require.Len(t, resp.UsersB, n)
		for i := 0; i < n; i++ {
			assertConn(resp.UsersA[i].ConnA, "usersA.connA", i)
			assertConn(resp.UsersA[i].ConnB, "usersA.connB", i)
			assertConn(resp.UsersB[i].ConnC, "usersB.connC", i)
			assertConn(resp.UsersB[i].ConnD, "usersB.connD", i)
		}

		require.Equal(t, int32(4), resolver.profileConnectionBatchCalls.Load(),
			"profileConnectionBatch should be called once per aliased field (2 roots × 2 aliases)")
		require.Equal(t, int32(4), resolver.coverBatchCalls.Load(),
			"coverBatch should be called once per aliased connection's profiles (4 independent scopes)")
	})
}

// TestBatchResolver_Nested_RecursiveUser tests 3 levels of the same type:
//
//	query {
//	  users {                              # level 0: 10 root users
//	    childUserBatch {                   # level 1: batch → 1 call, returns 10 L1 users
//	      childUserBatch {                 # level 2: batch → 1 call, returns 10 L2 users
//	        childUserBatch {               # level 3: batch → 1 call, returns 10 L3 users
//	          profileBatch { id }          # leaf:    batch → 1 call for all 10 L3 users
//	        }
//	      }
//	    }
//	  }
//	}
//
// Each level of childUserBatch should be called exactly once (not N times).
// The batching propagates through direct nesting: GetChildGroup creates a
// shared "User" BatchParentGroup from the batch results, and resolveField
// installs it into context for the next level.
func TestBatchResolver_Nested_RecursiveUser(t *testing.T) {
	const n = 10

	users := make([]*User, n)
	l1 := make([]*User, n) // level 1 child users
	l2 := make([]*User, n) // level 2 child users
	l3 := make([]*User, n) // level 3 child users
	profiles := make([]*Profile, n)
	images := make([]*Image, n)
	childMap := make(map[*User]*User, 3*n)
	allUserIndex := make(map[*User]int, 3*n)
	for i := 0; i < n; i++ {
		users[i] = &User{}
		l1[i] = &User{}
		l2[i] = &User{}
		l3[i] = &User{}
		profiles[i] = &Profile{ID: fmt.Sprintf("p%d", i)}
		images[i] = &Image{URL: fmt.Sprintf("https://img/%d", i)}
		// Chain: users[i] → l1[i] → l2[i] → l3[i]
		childMap[users[i]] = l1[i]
		childMap[l1[i]] = l2[i]
		childMap[l2[i]] = l3[i]
		// All levels map to the same logical index for profile lookup
		allUserIndex[l1[i]] = i
		allUserIndex[l2[i]] = i
		allUserIndex[l3[i]] = i
	}

	setup := func() (*Resolver, *client.Client) {
		r := &Resolver{
			users:         users,
			profiles:      profiles,
			images:        images,
			profileErrIdx: -1,
			childUserMap:  childMap,
			allUserIndex:  allUserIndex,
		}
		return r, newTestClient(r)
	}

	type graphqlResp struct {
		Users []struct {
			Child *struct {
				Child *struct {
					Child *struct {
						Profile *struct {
							ID string `json:"id"`
						} `json:"profile"`
					} `json:"child"`
				} `json:"child"`
			} `json:"child"`
		} `json:"users"`
	}

	assertData := func(t *testing.T, resp graphqlResp, label string) {
		t.Helper()
		require.Len(t, resp.Users, n)
		for i, u := range resp.Users {
			require.NotNil(t, u.Child, "%s user %d L1 nil", label, i)
			require.NotNil(t, u.Child.Child, "%s user %d L2 nil", label, i)
			require.NotNil(t, u.Child.Child.Child, "%s user %d L3 nil", label, i)
			require.NotNil(t, u.Child.Child.Child.Profile, "%s user %d L3 profile nil", label, i)
			require.Equal(t, fmt.Sprintf("p%d", i), u.Child.Child.Child.Profile.ID)
		}
	}

	// --- Batch path ---

	t.Run("batch", func(t *testing.T) {
		resolver, c := setup()

		var resp graphqlResp
		err := c.Post(`query {
			users {
				child: childUserBatch {
					child: childUserBatch {
						child: childUserBatch {
							profile: profileBatch { id }
						}
					}
				}
			}
		}`, &resp)
		require.NoError(t, err)
		assertData(t, resp, "batch")

		require.Equal(t, int32(3), resolver.childUserBatchCalls.Load(),
			"childUserBatch should be called once per level (3 levels)")
		require.Equal(t, int32(1), resolver.profileBatchCalls.Load(),
			"profileBatch should be called once for all L3 users")
	})

	// --- Non-batch path ---

	t.Run("non-batch", func(t *testing.T) {
		resolver, c := setup()

		var resp graphqlResp
		err := c.Post(`query {
			users {
				child: childUserNonBatch {
					child: childUserNonBatch {
						child: childUserNonBatch {
							profile: profileNonBatch { id }
						}
					}
				}
			}
		}`, &resp)
		require.NoError(t, err)
		assertData(t, resp, "non-batch")

		require.Equal(t, int32(n*3), resolver.childUserNonBatchCalls.Load(),
			"childUserNonBatch should be called once per user per level (10×3)")
		require.Equal(t, int32(n), resolver.profileNonBatchCalls.Load(),
			"profileNonBatch should be called once per L3 user")
	})

	// --- Verify both paths produce identical data ---

	t.Run("parity", func(t *testing.T) {
		_, c := setup()

		var batchResp, nonBatchResp graphqlResp
		err := c.Post(`query {
			users {
				child: childUserBatch {
					child: childUserBatch {
						child: childUserBatch {
							profile: profileBatch { id }
						}
					}
				}
			}
		}`, &batchResp)
		require.NoError(t, err)

		err = c.Post(`query {
			users {
				child: childUserNonBatch {
					child: childUserNonBatch {
						child: childUserNonBatch {
							profile: profileNonBatch { id }
						}
					}
				}
			}
		}`, &nonBatchResp)
		require.NoError(t, err)

		require.Equal(t,
			marshalJSON(t, batchResp),
			marshalJSON(t, nonBatchResp),
			"batch and non-batch should return identical data",
		)
	})
}

// TestBatchResolver_Nested_RecursiveUser_Mixed tests what happens when batch
// and non-batch resolvers are interleaved. A non-batch resolver in the chain
// breaks batch propagation for all downstream levels because it doesn't
// produce a BatchChildContext with a shared group.
//
// Compare with the fully-batched path (TestBatchResolver_Nested_RecursiveUser)
// where childUserBatch is called 3 times total. Here, inserting a non-batch
// resolver in the middle causes the downstream batch resolvers to fall back
// to one-call-per-parent.
func TestBatchResolver_Nested_RecursiveUser_Mixed(t *testing.T) {
	const n = 10

	users := make([]*User, n)
	l1 := make([]*User, n)
	l2 := make([]*User, n)
	l3 := make([]*User, n)
	profiles := make([]*Profile, n)
	childMap := make(map[*User]*User, 3*n)
	allUserIndex := make(map[*User]int, 3*n)
	for i := 0; i < n; i++ {
		users[i] = &User{}
		l1[i] = &User{}
		l2[i] = &User{}
		l3[i] = &User{}
		profiles[i] = &Profile{ID: fmt.Sprintf("p%d", i)}
		childMap[users[i]] = l1[i]
		childMap[l1[i]] = l2[i]
		childMap[l2[i]] = l3[i]
		allUserIndex[l1[i]] = i
		allUserIndex[l2[i]] = i
		allUserIndex[l3[i]] = i
	}

	setup := func() (*Resolver, *client.Client) {
		r := &Resolver{
			users:         users,
			profiles:      profiles,
			profileErrIdx: -1,
			childUserMap:  childMap,
			allUserIndex:  allUserIndex,
		}
		return r, newTestClient(r)
	}

	type graphqlResp struct {
		Users []struct {
			Child *struct {
				Child *struct {
					Child *struct {
						Profile *struct {
							ID string `json:"id"`
						} `json:"profile"`
					} `json:"child"`
				} `json:"child"`
			} `json:"child"`
		} `json:"users"`
	}

	assertData := func(t *testing.T, resp graphqlResp, label string) {
		t.Helper()
		require.Len(t, resp.Users, n)
		for i, u := range resp.Users {
			require.NotNil(t, u.Child, "%s user %d L1 nil", label, i)
			require.NotNil(t, u.Child.Child, "%s user %d L2 nil", label, i)
			require.NotNil(t, u.Child.Child.Child, "%s user %d L3 nil", label, i)
			require.NotNil(t, u.Child.Child.Child.Profile, "%s user %d profile nil", label, i)
			require.Equal(t, fmt.Sprintf("p%d", i), u.Child.Child.Child.Profile.ID)
		}
	}

	// Pattern: batch → nonBatch → batch → profileBatch
	//
	//   query {
	//     users {
	//       child: childUserBatch {            # batch: 1 call (all 10 users)
	//         child: childUserNonBatch {        # non-batch: 10 calls (breaks the chain)
	//           child: childUserBatch {         # batch: 10 calls (chain broken, single-parent fallback)
	//             profile: profileBatch { id }  # batch: 10 calls (chain broken)
	//           }
	//         }
	//       }
	//     }
	//   }
	//
	// The non-batch resolver at level 2 breaks the batch chain. Although the
	// L1 "User" group and batchResultIndex survive in context, level 3's
	// resolveBatch checks that parents[idx] == obj. Since obj is an L2 user
	// but parents[idx] is an L1 user, the identity check fails and the
	// resolver falls back to single-parent. Without this check, level 3
	// would incorrectly resolve for L1 parents instead of L2 parents.
	t.Run("batch-nonBatch-batch", func(t *testing.T) {
		resolver, c := setup()

		var resp graphqlResp
		err := c.Post(`query {
			users {
				child: childUserBatch {
					child: childUserNonBatch {
						child: childUserBatch {
							profile: profileBatch { id }
						}
					}
				}
			}
		}`, &resp)
		require.NoError(t, err)
		assertData(t, resp, "batch-nonBatch-batch")

		require.Equal(t, int32(1+n), resolver.childUserBatchCalls.Load(),
			"childUserBatch: 1 call at level 1 (batched) + 10 calls at level 3 (chain broken)")
		require.Equal(t, int32(n), resolver.childUserNonBatchCalls.Load(),
			"childUserNonBatch: 10 calls at level 2 (one per L1 user)")
		require.Equal(t, int32(n), resolver.profileBatchCalls.Load(),
			"profileBatch: 10 calls (chain broken by non-batch at level 2)")
	})

	// Pattern: nonBatch → batch → batch → profileBatch
	//
	//   query {
	//     users {
	//       child: childUserNonBatch {          # non-batch: 10 calls (breaks the chain immediately)
	//         child: childUserBatch {            # batch: 10 calls (no shared group from non-batch parent)
	//           child: childUserBatch {          # batch: 10 calls (still broken)
	//             profile: profileBatch { id }   # batch: 10 calls (still broken)
	//           }
	//         }
	//       }
	//     }
	//   }
	//
	// Once broken at level 1, the chain stays broken: even though levels 2
	// and 3 are both batch resolvers, each runs in the single-parent fallback
	// path (ResolveBatchSingleResult) which doesn't create a BatchChildContext.
	t.Run("nonBatch-batch-batch", func(t *testing.T) {
		resolver, c := setup()

		var resp graphqlResp
		err := c.Post(`query {
			users {
				child: childUserNonBatch {
					child: childUserBatch {
						child: childUserBatch {
							profile: profileBatch { id }
						}
					}
				}
			}
		}`, &resp)
		require.NoError(t, err)
		assertData(t, resp, "nonBatch-batch-batch")

		require.Equal(t, int32(n), resolver.childUserNonBatchCalls.Load(),
			"childUserNonBatch: 10 calls at level 1")
		require.Equal(t, int32(n+n), resolver.childUserBatchCalls.Load(),
			"childUserBatch: 10 calls at level 2 + 10 calls at level 3 (chain broken at level 1)")
		require.Equal(t, int32(n), resolver.profileBatchCalls.Load(),
			"profileBatch: 10 calls (chain broken)")
	})

	// Pattern: batch → batch → nonBatch → profileNonBatch
	//
	//   query {
	//     users {
	//       child: childUserBatch {             # batch: 1 call
	//         child: childUserBatch {           # batch: 1 call (chain intact)
	//           child: childUserNonBatch {      # non-batch: 10 calls (chain was intact until here)
	//             profile: profileNonBatch { id}# non-batch: 10 calls
	//           }
	//         }
	//       }
	//     }
	//   }
	//
	// The first two levels batch correctly (1 call each). The non-batch at
	// level 3 is called 10 times but that's expected — it's not a batch
	// resolver. This shows the chain works fine until a non-batch resolver
	// is the consumer.
	t.Run("batch-batch-nonBatch", func(t *testing.T) {
		resolver, c := setup()

		var resp graphqlResp
		err := c.Post(`query {
			users {
				child: childUserBatch {
					child: childUserBatch {
						child: childUserNonBatch {
							profile: profileNonBatch { id }
						}
					}
				}
			}
		}`, &resp)
		require.NoError(t, err)
		assertData(t, resp, "batch-batch-nonBatch")

		require.Equal(t, int32(2), resolver.childUserBatchCalls.Load(),
			"childUserBatch: 1 call at level 1 + 1 call at level 2 (chain intact)")
		require.Equal(t, int32(n), resolver.childUserNonBatchCalls.Load(),
			"childUserNonBatch: 10 calls at level 3 (not a batch resolver)")
		require.Equal(t, int32(n), resolver.profileNonBatchCalls.Load(),
			"profileNonBatch: 10 calls (not a batch resolver)")
	})

	// Data parity: all mixed patterns produce the same data as the
	// fully-batched and fully-non-batched paths.
	t.Run("parity", func(t *testing.T) {
		_, c := setup()

		queries := []struct {
			name  string
			query string
		}{
			{"all-batch", `query { users { child: childUserBatch { child: childUserBatch { child: childUserBatch { profile: profileBatch { id } } } } } }`},
			{"all-nonBatch", `query { users { child: childUserNonBatch { child: childUserNonBatch { child: childUserNonBatch { profile: profileNonBatch { id } } } } } }`},
			{"batch-nonBatch-batch", `query { users { child: childUserBatch { child: childUserNonBatch { child: childUserBatch { profile: profileBatch { id } } } } } }`},
			{"nonBatch-batch-batch", `query { users { child: childUserNonBatch { child: childUserBatch { child: childUserBatch { profile: profileBatch { id } } } } } }`},
			{"batch-batch-nonBatch", `query { users { child: childUserBatch { child: childUserBatch { child: childUserNonBatch { profile: profileNonBatch { id } } } } } }`},
		}

		results := make([]string, len(queries))
		for i, q := range queries {
			var resp graphqlResp
			err := c.Post(q.query, &resp)
			require.NoError(t, err, q.name)
			results[i] = marshalJSON(t, resp)
		}

		for i := 1; i < len(queries); i++ {
			require.Equal(t, results[0], results[i],
				"%s should return the same data as %s", queries[i].name, queries[0].name)
		}
	})
}

func BenchmarkBatchResolver_SingleLevel(b *testing.B) {
	const n = 100
	users := make([]*User, n)
	profiles := make([]*Profile, n)
	for i := 0; i < n; i++ {
		users[i] = &User{}
		profiles[i] = &Profile{ID: fmt.Sprintf("p%d", i)}
	}

	b.Run("batch", func(b *testing.B) {
		resolver := &Resolver{
			users:         users,
			profiles:      profiles,
			profileErrIdx: -1,
		}
		c := newTestClient(resolver)
		var resp json.RawMessage
		for b.Loop() {
			_ = c.Post(`query { users { nullableBatch { id } } }`, &resp)
		}
	})

	b.Run("non-batch", func(b *testing.B) {
		resolver := &Resolver{
			users:         users,
			profiles:      profiles,
			profileErrIdx: -1,
		}
		c := newTestClient(resolver)
		var resp json.RawMessage
		for b.Loop() {
			_ = c.Post(`query { users { nullableNonBatch { id } } }`, &resp)
		}
	})
}

func BenchmarkBatchResolver_Nested(b *testing.B) {
	const n = 100
	users := make([]*User, n)
	profiles := make([]*Profile, n)
	images := make([]*Image, n)
	for i := 0; i < n; i++ {
		users[i] = &User{}
		profiles[i] = &Profile{ID: fmt.Sprintf("p%d", i)}
		images[i] = &Image{URL: fmt.Sprintf("https://img/%d", i)}
	}

	b.Run("batch", func(b *testing.B) {
		resolver := &Resolver{
			users:         users,
			profiles:      profiles,
			images:        images,
			profileErrIdx: -1,
		}
		c := newTestClient(resolver)
		var resp json.RawMessage
		for b.Loop() {
			_ = c.Post(`query {
				users {
					profile: profileBatch {
						id
						cover: coverBatch { url }
					}
				}
			}`, &resp)
		}
	})

	b.Run("non-batch", func(b *testing.B) {
		resolver := &Resolver{
			users:         users,
			profiles:      profiles,
			images:        images,
			profileErrIdx: -1,
		}
		c := newTestClient(resolver)
		var resp json.RawMessage
		for b.Loop() {
			_ = c.Post(`query {
				users {
					profile: profileNonBatch {
						id
						cover: coverNonBatch { url }
					}
				}
			}`, &resp)
		}
	})
}
