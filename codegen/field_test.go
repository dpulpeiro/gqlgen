package codegen

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2"
	ast2 "github.com/vektah/gqlparser/v2/ast"

	"github.com/99designs/gqlgen/codegen/config"
	"github.com/99designs/gqlgen/internal/code"
)

func TestFindField(t *testing.T) {
	input := `
package test

type Std struct {
	Name string
	Value int
}
type Anon struct {
	Name string
	Tags
}
type Tags struct {
	Bar string ` + "`" + `gqlgen:"foo"` + "`" + `
	Foo int    ` + "`" + `gqlgen:"bar"` + "`" + `
}
type Amb struct {
	Bar string ` + "`" + `gqlgen:"foo"` + "`" + `
	Foo int    ` + "`" + `gqlgen:"foo"` + "`" + `
}
type Embed struct {
	Std
	Test string
}
`
	scope, err := parseScope(input, "test")
	require.NoError(t, err)

	std := scope.Lookup("Std").Type().(*types.Named)
	anon := scope.Lookup("Anon").Type().(*types.Named)
	tags := scope.Lookup("Tags").Type().(*types.Named)
	amb := scope.Lookup("Amb").Type().(*types.Named)
	embed := scope.Lookup("Embed").Type().(*types.Named)

	tests := []struct {
		Name        string
		Named       *types.Named
		Field       string
		Tag         string
		Expected    string
		ShouldError bool
	}{
		{"Finds a field by name with no tag", std, "name", "", "Name", false},
		{
			"Finds a field by name when passed tag but tag not used",
			std,
			"name",
			"gqlgen",
			"Name",
			false,
		},
		{"Ignores tags when not passed a tag", tags, "foo", "", "Foo", false},
		{
			"Picks field with tag over field name when passed a tag",
			tags,
			"foo",
			"gqlgen",
			"Bar",
			false,
		},
		{"Errors when ambiguous", amb, "foo", "gqlgen", "", true},
		{"Finds a field that is in embedded struct", anon, "bar", "", "Bar", false},
		{"Finds field that is not in embedded struct", embed, "test", "", "Test", false},
	}

	for _, tt := range tests {
		b := builder{Config: &config.Config{StructTag: tt.Tag}}
		target, err := b.findBindTarget(tt.Named, tt.Field, false)
		if tt.ShouldError {
			require.Nil(t, target, tt.Name)
			require.Error(t, err, tt.Name)
		} else {
			require.NoError(t, err, tt.Name)
			require.Equal(t, tt.Expected, target.Name(), tt.Name)
		}
	}
}

func parseScope(input any, packageName string) (*types.Scope, error) {
	// test setup to parse the types
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", input, 0)
	if err != nil {
		return nil, err
	}

	conf := types.Config{Importer: importer.Default()}
	pkg, err := conf.Check(packageName, fset, []*ast.File{f}, nil)
	if err != nil {
		return nil, err
	}

	return pkg.Scope(), nil
}

func TestEqualFieldName(t *testing.T) {
	tt := []struct {
		Name     string
		Source   string
		Target   string
		Expected bool
	}{
		{Name: "words with same case", Source: "test", Target: "test", Expected: true},
		{Name: "words different case", Source: "test", Target: "tEsT", Expected: true},
		{Name: "different words", Source: "foo", Target: "bar", Expected: false},
		{Name: "separated with underscore", Source: "the_test", Target: "TheTest", Expected: true},
		{Name: "empty values", Source: "", Target: "", Expected: true},
	}

	for _, tc := range tt {
		t.Run(tc.Name, func(t *testing.T) {
			result := equalFieldName(tc.Source, tc.Target)
			require.Equal(t, tc.Expected, result)
		})
	}
}

func TestField_Batch(t *testing.T) {
	t.Run("Batch flag defaults to false", func(t *testing.T) {
		f := Field{}
		require.False(t, f.Batch)
		require.False(t, f.IsBatch())
	})

	t.Run("Batch flag can be set", func(t *testing.T) {
		f := Field{Batch: true}
		require.True(t, f.Batch)
		require.True(t, f.IsBatch())
	})
}

func TestField_BatchRootFieldUnsupported(t *testing.T) {
	cfg := &config.Config{
		Exec: config.ExecConfig{
			Layout:   config.ExecLayoutSingleFile,
			Filename: "generated.go",
			Package:  "generated",
		},
		Models: config.TypeMap{
			"Query": {
				Fields: map[string]config.TypeMapField{
					"version": {Batch: true},
				},
			},
			"Boolean": {
				Model: config.StringList{"github.com/99designs/gqlgen/graphql.Boolean"},
			},
			"Float": {
				Model: config.StringList{"github.com/99designs/gqlgen/graphql.Float"},
			},
			"ID": {
				Model: config.StringList{"github.com/99designs/gqlgen/graphql.ID"},
			},
			"Int": {
				Model: config.StringList{"github.com/99designs/gqlgen/graphql.Int"},
			},
			"String": {
				Model: config.StringList{"github.com/99designs/gqlgen/graphql.String"},
			},
		},
		Directives: map[string]config.DirectiveConfig{},
		Packages:   code.NewPackages(),
	}
	cfg.Schema = gqlparser.MustLoadSchema(&ast2.Source{
		Name: "schema.graphql",
		Input: `
			schema { query: Query }
			type Query { version: String }
		`,
	})

	b := builder{
		Config: cfg,
		Schema: cfg.Schema,
	}
	b.Binder = b.Config.NewBinder()
	var err error
	b.Directives, err = b.buildDirectives()
	require.NoError(t, err)

	_, err = b.buildObject(cfg.Schema.Query)
	require.Error(t, err)
	require.Contains(t, err.Error(), "batch resolver is not supported for root field Query.version")
}

func TestField_CallArgs(t *testing.T) {
	tt := []struct {
		Name string
		Field
		Expected string
	}{
		{
			Name: "Field with method that has context, and three args (string, interface, named interface)",
			Field: Field{
				MethodHasContext: true,
				Args: []*FieldArgument{
					{
						ArgumentDefinition: &ast2.ArgumentDefinition{
							Name: "test",
						},
						TypeReference: &config.TypeReference{
							GO: (&types.Interface{}).Complete(),
						},
					},
					{
						ArgumentDefinition: &ast2.ArgumentDefinition{
							Name: "test2",
						},
						TypeReference: &config.TypeReference{
							GO: types.NewNamed(
								types.NewTypeName(token.NoPos, nil, "TestInterface", nil),
								(&types.Interface{}).Complete(),
								nil,
							),
						},
					},
					{
						ArgumentDefinition: &ast2.ArgumentDefinition{
							Name: "test3",
						},
						TypeReference: &config.TypeReference{
							GO: types.Typ[types.String],
						},
					},
				},
			},
			Expected: `ctx, ` + `
				func () any {
					if fc.Args["test"] == nil {
						return nil
					}
					return fc.Args["test"].(any)
				}(), fc.Args["test2"].(TestInterface), fc.Args["test3"].(string)`,
		},
		{
			Name: "Resolver field that isn't root object with single int argument",
			Field: Field{
				Object: &Object{
					Root: false,
				},
				IsResolver: true,
				Args: []*FieldArgument{
					{
						ArgumentDefinition: &ast2.ArgumentDefinition{
							Name: "test",
						},
						TypeReference: &config.TypeReference{
							GO: types.Typ[types.Int],
						},
					},
				},
			},
			Expected: `ctx, obj, fc.Args["test"].(int)`,
		},
	}

	for _, tc := range tt {
		t.Run(tc.Name, func(t *testing.T) {
			require.Equal(t, tc.Expected, tc.CallArgs())
		})
	}
}

func TestField_BatchCallArgs(t *testing.T) {
	tt := []struct {
		Name     string
		Field    Field
		Expected string
	}{
		{
			Name: "Batch args with single int argument",
			Field: Field{
				Args: []*FieldArgument{
					{
						ArgumentDefinition: &ast2.ArgumentDefinition{
							Name: "test",
						},
						TypeReference: &config.TypeReference{
							GO: types.Typ[types.Int],
						},
					},
				},
			},
			Expected: `ctx, parents, fc.Args["test"].(int)`,
		},
		{
			Name: "Batch args with empty interface and string",
			Field: Field{
				Args: []*FieldArgument{
					{
						ArgumentDefinition: &ast2.ArgumentDefinition{
							Name: "test",
						},
						TypeReference: &config.TypeReference{
							GO: (&types.Interface{}).Complete(),
						},
					},
					{
						ArgumentDefinition: &ast2.ArgumentDefinition{
							Name: "test2",
						},
						TypeReference: &config.TypeReference{
							GO: types.Typ[types.String],
						},
					},
				},
			},
			Expected: `ctx, parents, ` + `
				func () any {
					if fc.Args["test"] == nil {
						return nil
					}
					return fc.Args["test"].(any)
				}(), fc.Args["test2"].(string)`,
		},
	}

	for _, tc := range tt {
		t.Run(tc.Name, func(t *testing.T) {
			require.Equal(t, tc.Expected, tc.Field.BatchCallArgs("parents"))
		})
	}
}

func TestField_ShortBatchResolverDeclaration(t *testing.T) {
	f := Field{
		FieldDefinition: &ast2.FieldDefinition{
			Name: "value",
		},
		Object: &Object{
			Definition: &ast2.Definition{
				Name: "User",
			},
			Type: types.Typ[types.Int],
		},
		TypeReference: &config.TypeReference{
			GO: types.Typ[types.String],
		},
		Args: []*FieldArgument{
			{
				ArgumentDefinition: &ast2.ArgumentDefinition{
					Name: "limit",
				},
				VarName: "limit",
				TypeReference: &config.TypeReference{
					GO: types.Typ[types.Int],
				},
			},
		},
	}

	require.Equal(
		t,
		"(ctx context.Context, objs []*int, limit int) ([]string, error)",
		f.ShortBatchResolverDeclaration(),
	)
}

func TestField_NestedBatchPaths_NilGoType(t *testing.T) {
	// Schema where Connection → edges: [Edge!]! → node: Profile,
	// and Profile has batch fields in models but NO corresponding Object
	// in the objects list. This simulates an external/federation type
	// that has no codegen object — goTypes[typeName] will be nil.
	schema := gqlparser.MustLoadSchema(&ast2.Source{
		Name: "test.graphql",
		Input: `
			type Connection {
				edges: [Edge!]!
			}
			type Edge {
				node: Profile!
			}
			type Profile {
				id: ID!
				coverBatch: Image
			}
			type Image {
				url: String!
			}
		`,
	})

	models := config.TypeMap{
		"Profile": {
			Fields: map[string]config.TypeMapField{
				"coverBatch": {Batch: true},
			},
		},
	}

	// Objects list does NOT include Profile — simulates external model with no Go type.
	objects := Objects{
		{
			Definition: schema.Types["Connection"],
			Type:       types.Typ[types.Int],
		},
		{
			Definition: schema.Types["Edge"],
			Type:       types.Typ[types.Int],
		},
	}

	f := Field{
		TypeReference: &config.TypeReference{
			Definition: schema.Types["Connection"],
		},
	}

	// Should not panic and should return no paths (Profile has no Go type).
	paths := computeNestedBatchPaths(&f, schema, models, objects)
	require.Empty(t, paths)
}

func TestField_NestedBatchPaths_WithGoType(t *testing.T) {
	// Profile IS in the objects list with field bindings on intermediate types.
	// Verifies the path is found using actual Go field names from bindings.
	schema := gqlparser.MustLoadSchema(&ast2.Source{
		Name: "test.graphql",
		Input: `
			type Connection {
				edges: [Edge!]!
			}
			type Edge {
				node: Profile!
			}
			type Profile {
				id: ID!
				coverBatch: Image
			}
			type Image {
				url: String!
			}
		`,
	})

	models := config.TypeMap{
		"Profile": {
			Fields: map[string]config.TypeMapField{
				"coverBatch": {Batch: true},
			},
		},
	}

	profileType := types.NewNamed(
		types.NewTypeName(token.NoPos, nil, "Profile", nil),
		types.NewStruct(nil, nil),
		nil,
	)

	objects := Objects{
		{
			Definition: schema.Types["Connection"],
			Type:       types.Typ[types.Int],
			Fields: []*Field{
				{
					FieldDefinition: &ast2.FieldDefinition{Name: "edges"},
					GoFieldName:     "Edges",
					GoFieldType:     GoFieldVariable,
				},
			},
		},
		{
			Definition: schema.Types["Edge"],
			Type:       types.Typ[types.Int],
			Fields: []*Field{
				{
					FieldDefinition: &ast2.FieldDefinition{Name: "node"},
					GoFieldName:     "Node",
					GoFieldType:     GoFieldVariable,
				},
			},
		},
		{
			Definition: schema.Types["Profile"],
			Type:       profileType,
		},
	}

	f := Field{
		TypeReference: &config.TypeReference{
			Definition: schema.Types["Connection"],
		},
	}

	paths := computeNestedBatchPaths(&f, schema, models, objects)
	require.Len(t, paths, 1)
	require.Equal(t, "Profile", paths[0].TargetTypeName)
	require.Len(t, paths[0].Steps, 2)
	require.Equal(t, "Edges", paths[0].Steps[0].GoFieldName)
	require.True(t, paths[0].Steps[0].IsList)
	require.Equal(t, "Node", paths[0].Steps[1].GoFieldName)
	require.False(t, paths[0].Steps[1].IsList)
}

func TestField_NestedBatchPaths_CrossFile(t *testing.T) {
	// In follow-schema layout, per-file Objects only contain types from that
	// schema file. Nested batch paths are precomputed in BuildData when all
	// objects are available. This test verifies that paths are found when the
	// target batch type (Profile) is only present in the full object set.
	schema := gqlparser.MustLoadSchema(&ast2.Source{
		Name: "test.graphql",
		Input: `
			type Connection {
				edges: [Edge!]!
			}
			type Edge {
				node: Profile!
			}
			type Profile {
				id: ID!
				coverBatch: Image
			}
			type Image {
				url: String!
			}
		`,
	})

	models := config.TypeMap{
		"Profile": {
			Fields: map[string]config.TypeMapField{
				"coverBatch": {Batch: true},
			},
		},
	}

	profileType := types.NewNamed(
		types.NewTypeName(token.NoPos, nil, "Profile", nil),
		types.NewStruct(nil, nil),
		nil,
	)

	// Simulate per-file Objects for connection.graphql — only Connection and Edge.
	perFileObjects := Objects{
		{
			Definition: schema.Types["Connection"],
			Type:       types.Typ[types.Int],
			Fields: []*Field{
				{
					FieldDefinition: &ast2.FieldDefinition{Name: "edges"},
					GoFieldName:     "Edges",
					GoFieldType:     GoFieldVariable,
				},
			},
		},
		{
			Definition: schema.Types["Edge"],
			Type:       types.Typ[types.Int],
			Fields: []*Field{
				{
					FieldDefinition: &ast2.FieldDefinition{Name: "node"},
					GoFieldName:     "Node",
					GoFieldType:     GoFieldVariable,
				},
			},
		},
	}

	// Simulate full object set — includes Profile from profile.graphql.
	allObjects := make(Objects, len(perFileObjects), len(perFileObjects)+1)
	copy(allObjects, perFileObjects)
	allObjects = append(allObjects, &Object{
		Definition: schema.Types["Profile"],
		Type:       profileType,
	})

	f := Field{
		TypeReference: &config.TypeReference{
			Definition: schema.Types["Connection"],
		},
	}

	// With only per-file objects (no Profile), path cannot be resolved.
	paths := computeNestedBatchPaths(&f, schema, models, perFileObjects)
	require.Empty(t, paths, "per-file objects should not find cross-file batch target")

	// With full object set (includes Profile), the path is found.
	paths = computeNestedBatchPaths(&f, schema, models, allObjects)
	require.Len(t, paths, 1)
	require.Equal(t, "Profile", paths[0].TargetTypeName)
	require.Len(t, paths[0].Steps, 2)
	require.Equal(t, "Edges", paths[0].Steps[0].GoFieldName)
	require.True(t, paths[0].Steps[0].IsList)
	require.Equal(t, "Node", paths[0].Steps[1].GoFieldName)
	require.False(t, paths[0].Steps[1].IsList)
}

func TestField_NestedBatchPaths_SkipsResolverFields(t *testing.T) {
	// When an intermediate field is a resolver (not a direct struct field),
	// the path through that field must be skipped because the generated
	// template uses direct struct field access (v.FieldName).
	schema := gqlparser.MustLoadSchema(&ast2.Source{
		Name: "test.graphql",
		Input: `
			type Connection {
				edges: [Edge!]!
			}
			type Edge {
				node: Profile!
			}
			type Profile {
				id: ID!
				coverBatch: Image
			}
			type Image {
				url: String!
			}
		`,
	})

	models := config.TypeMap{
		"Profile": {
			Fields: map[string]config.TypeMapField{
				"coverBatch": {Batch: true},
			},
		},
	}

	profileType := types.NewNamed(
		types.NewTypeName(token.NoPos, nil, "Profile", nil),
		types.NewStruct(nil, nil),
		nil,
	)

	objects := Objects{
		{
			Definition: schema.Types["Connection"],
			Type:       types.Typ[types.Int],
			Fields: []*Field{
				{
					FieldDefinition: &ast2.FieldDefinition{Name: "edges"},
					GoFieldName:     "Edges",
					GoFieldType:     GoFieldVariable,
				},
			},
		},
		{
			Definition: schema.Types["Edge"],
			Type:       types.Typ[types.Int],
			Fields: []*Field{
				{
					FieldDefinition: &ast2.FieldDefinition{Name: "node"},
					GoFieldName:     "Node",
					IsResolver:      true, // resolver field — cannot be accessed as v.Node
				},
			},
		},
		{
			Definition: schema.Types["Profile"],
			Type:       profileType,
		},
	}

	f := Field{
		TypeReference: &config.TypeReference{
			Definition: schema.Types["Connection"],
		},
	}

	// Path through Edge.node is skipped because node is a resolver.
	paths := computeNestedBatchPaths(&f, schema, models, objects)
	require.Empty(t, paths)
}

func TestField_NestedBatchPaths_UnionAsStartingPoint(t *testing.T) {
	// Batch field returns a union type. Each union member that has batch
	// fields should produce a path with a type-assertion step.
	schema := gqlparser.MustLoadSchema(&ast2.Source{
		Name: "test.graphql",
		Input: `
			union SearchResult = Article | Video
			type Article {
				id: ID!
			}
			type Video {
				id: ID!
				tags: [Tag!]
			}
			type Tag {
				id: ID!
			}
		`,
	})

	models := config.TypeMap{
		"Video": {
			Fields: map[string]config.TypeMapField{
				"tags": {Batch: true},
			},
		},
	}

	videoType := types.NewNamed(
		types.NewTypeName(token.NoPos, nil, "Video", nil),
		types.NewStruct(nil, nil),
		nil,
	)
	articleType := types.NewNamed(
		types.NewTypeName(token.NoPos, nil, "Article", nil),
		types.NewStruct(nil, nil),
		nil,
	)

	objects := Objects{
		{
			Definition: schema.Types["Video"],
			Type:       videoType,
		},
		{
			Definition: schema.Types["Article"],
			Type:       articleType,
		},
	}

	f := Field{
		TypeReference: &config.TypeReference{
			Definition: schema.Types["SearchResult"],
		},
	}

	paths := computeNestedBatchPaths(&f, schema, models, objects)
	require.Len(t, paths, 1)
	require.Equal(t, "Video", paths[0].TargetTypeName)
	require.Len(t, paths[0].Steps, 1)
	require.True(t, paths[0].Steps[0].IsTypeAssert)
	// Reference() wraps non-nilable types in a pointer.
	require.Equal(t, types.NewPointer(videoType), paths[0].Steps[0].AssertGoType)
}

func TestField_NestedBatchPaths_UnionDuringTraversal(t *testing.T) {
	// A struct field points to a union type. The path should include the
	// field-access step followed by a type-assertion step.
	schema := gqlparser.MustLoadSchema(&ast2.Source{
		Name: "test.graphql",
		Input: `
			type Container {
				item: SearchResult
			}
			union SearchResult = Article | Video
			type Article {
				id: ID!
			}
			type Video {
				id: ID!
				tags: [Tag!]
			}
			type Tag {
				id: ID!
			}
		`,
	})

	models := config.TypeMap{
		"Video": {
			Fields: map[string]config.TypeMapField{
				"tags": {Batch: true},
			},
		},
	}

	videoType := types.NewNamed(
		types.NewTypeName(token.NoPos, nil, "Video", nil),
		types.NewStruct(nil, nil),
		nil,
	)
	articleType := types.NewNamed(
		types.NewTypeName(token.NoPos, nil, "Article", nil),
		types.NewStruct(nil, nil),
		nil,
	)

	objects := Objects{
		{
			Definition: schema.Types["Container"],
			Type:       types.Typ[types.Int],
			Fields: []*Field{
				{
					FieldDefinition: &ast2.FieldDefinition{Name: "item"},
					GoFieldName:     "Item",
					GoFieldType:     GoFieldVariable,
				},
			},
		},
		{
			Definition: schema.Types["Video"],
			Type:       videoType,
		},
		{
			Definition: schema.Types["Article"],
			Type:       articleType,
		},
	}

	f := Field{
		TypeReference: &config.TypeReference{
			Definition: schema.Types["Container"],
		},
	}

	paths := computeNestedBatchPaths(&f, schema, models, objects)
	require.Len(t, paths, 1)
	require.Equal(t, "Video", paths[0].TargetTypeName)
	require.Len(t, paths[0].Steps, 2)
	// First step: field access to "Item"
	require.Equal(t, "Item", paths[0].Steps[0].GoFieldName)
	require.False(t, paths[0].Steps[0].IsTypeAssert)
	// Second step: type assertion to Video
	require.True(t, paths[0].Steps[1].IsTypeAssert)
	require.Equal(t, types.NewPointer(videoType), paths[0].Steps[1].AssertGoType)
}

func TestField_NestedBatchPaths_UnionMemberWithoutBatchFields(t *testing.T) {
	// Union where no member has batch fields but a member has a child
	// object with batch fields — should recurse through the member.
	schema := gqlparser.MustLoadSchema(&ast2.Source{
		Name: "test.graphql",
		Input: `
			union ResultType = Wrapper
			type Wrapper {
				profile: Profile!
			}
			type Profile {
				id: ID!
				coverBatch: Image
			}
			type Image {
				url: String!
			}
		`,
	})

	models := config.TypeMap{
		"Profile": {
			Fields: map[string]config.TypeMapField{
				"coverBatch": {Batch: true},
			},
		},
	}

	profileType := types.NewNamed(
		types.NewTypeName(token.NoPos, nil, "Profile", nil),
		types.NewStruct(nil, nil),
		nil,
	)
	wrapperType := types.NewNamed(
		types.NewTypeName(token.NoPos, nil, "Wrapper", nil),
		types.NewStruct(nil, nil),
		nil,
	)

	objects := Objects{
		{
			Definition: schema.Types["Wrapper"],
			Type:       wrapperType,
			Fields: []*Field{
				{
					FieldDefinition: &ast2.FieldDefinition{Name: "profile"},
					GoFieldName:     "Profile",
					GoFieldType:     GoFieldVariable,
				},
			},
		},
		{
			Definition: schema.Types["Profile"],
			Type:       profileType,
		},
	}

	f := Field{
		TypeReference: &config.TypeReference{
			Definition: schema.Types["ResultType"],
		},
	}

	paths := computeNestedBatchPaths(&f, schema, models, objects)
	require.Len(t, paths, 1)
	require.Equal(t, "Profile", paths[0].TargetTypeName)
	require.Len(t, paths[0].Steps, 2)
	// First step: type assertion to Wrapper
	require.True(t, paths[0].Steps[0].IsTypeAssert)
	require.Equal(t, types.NewPointer(wrapperType), paths[0].Steps[0].AssertGoType)
	// Second step: field access to Profile
	require.Equal(t, "Profile", paths[0].Steps[1].GoFieldName)
	require.False(t, paths[0].Steps[1].IsTypeAssert)
}

func TestField_NestedBatchPaths_UnionMemberNilGoType(t *testing.T) {
	// Union member exists in schema but has no Go type in objects.
	// Should be skipped gracefully.
	schema := gqlparser.MustLoadSchema(&ast2.Source{
		Name: "test.graphql",
		Input: `
			union SearchResult = Article | Video
			type Article {
				id: ID!
			}
			type Video {
				id: ID!
				tags: [Tag!]
			}
			type Tag {
				id: ID!
			}
		`,
	})

	models := config.TypeMap{
		"Video": {
			Fields: map[string]config.TypeMapField{
				"tags": {Batch: true},
			},
		},
	}

	// No objects provided — all Go types are nil.
	objects := Objects{}

	f := Field{
		TypeReference: &config.TypeReference{
			Definition: schema.Types["SearchResult"],
		},
	}

	// Should not panic and should return no paths.
	paths := computeNestedBatchPaths(&f, schema, models, objects)
	require.Empty(t, paths)
}
