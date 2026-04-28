package module

import (
	"fmt"
	"strings"

	"github.com/darkrain/request-generator/actions"
	"github.com/darkrain/request-generator/fields"
	pg "github.com/go-jet/jet/v2/postgres"
)

// ============================================
// OpenAPI 3.0.3 struct definitions
// ============================================

type OpenAPISpec struct {
	OpenAPI    string                     `json:"openapi"`
	Info       OpenAPIInfo                `json:"info"`
	Paths      map[string]OpenAPIPathItem `json:"paths"`
	Components *OpenAPIComponents         `json:"components,omitempty"`
}

type OpenAPIInfo struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version"`
}

type OpenAPIComponents struct {
	Schemas         map[string]OpenAPISchema        `json:"schemas,omitempty"`
	SecuritySchemes map[string]OpenAPISecurityScheme `json:"securitySchemes,omitempty"`
}

type OpenAPISecurityScheme struct {
	Type         string `json:"type"`
	Scheme       string `json:"scheme,omitempty"`
	BearerFormat string `json:"bearerFormat,omitempty"`
}

type OpenAPIPathItem struct {
	Get    *OpenAPIOperation `json:"get,omitempty"`
	Put    *OpenAPIOperation `json:"put,omitempty"`
	Post   *OpenAPIOperation `json:"post,omitempty"`
	Delete *OpenAPIOperation `json:"delete,omitempty"`
}

type OpenAPIOperation struct {
	Tags        []string                   `json:"tags,omitempty"`
	Summary     string                     `json:"summary,omitempty"`
	Description string                     `json:"description,omitempty"`
	OperationID string                     `json:"operationId,omitempty"`
	Parameters  []OpenAPIParameter         `json:"parameters,omitempty"`
	RequestBody *OpenAPIRequestBody        `json:"requestBody,omitempty"`
	Responses   map[string]OpenAPIResponse `json:"responses"`
	Security    []map[string][]string      `json:"security,omitempty"`
}

type OpenAPIParameter struct {
	Name        string         `json:"name"`
	In          string         `json:"in"`
	Description string         `json:"description,omitempty"`
	Required    bool           `json:"required,omitempty"`
	Schema      *OpenAPISchema `json:"schema,omitempty"`
}

type OpenAPIRequestBody struct {
	Required bool                        `json:"required,omitempty"`
	Content  map[string]OpenAPIMediaType `json:"content"`
}

type OpenAPIMediaType struct {
	Schema *OpenAPISchema `json:"schema"`
}

type OpenAPIResponse struct {
	Description string                      `json:"description"`
	Content     map[string]OpenAPIMediaType `json:"content,omitempty"`
}

type OpenAPISchema struct {
	Ref         string                    `json:"$ref,omitempty"`
	Type        string                    `json:"type,omitempty"`
	Format      string                    `json:"format,omitempty"`
	Description string                    `json:"description,omitempty"`
	Properties  map[string]*OpenAPISchema `json:"properties,omitempty"`
	Items       *OpenAPISchema            `json:"items,omitempty"`
	Required    []string                  `json:"required,omitempty"`
	Enum        []interface{}             `json:"enum,omitempty"`
	MinLength   *int                      `json:"minLength,omitempty"`
	MaxLength   *int                      `json:"maxLength,omitempty"`
	Example     interface{}               `json:"example,omitempty"`
}

// ============================================
// Spec builder
// ============================================

func (generator *Generator) buildOpenAPISpec(title, version string) OpenAPISpec {
	spec := OpenAPISpec{
		OpenAPI: "3.0.3",
		Info: OpenAPIInfo{
			Title:   title,
			Version: version,
		},
		Paths: make(map[string]OpenAPIPathItem),
		Components: &OpenAPIComponents{
			Schemas:         make(map[string]OpenAPISchema),
			SecuritySchemes: make(map[string]OpenAPISecurityScheme),
		},
	}

	spec.Components.SecuritySchemes["bearerAuth"] = OpenAPISecurityScheme{
		Type:         "http",
		Scheme:       "bearer",
		BearerFormat: "JWT",
	}

	spec.Components.Schemas["ErrorResponse"] = OpenAPISchema{
		Type: "object",
		Properties: map[string]*OpenAPISchema{
			"statusCode": {Type: "integer"},
			"message":    {Type: "string"},
			"errors":     {Type: "object"},
		},
	}

	spec.Components.Schemas["DeleteResponse"] = OpenAPISchema{
		Type: "object",
		Properties: map[string]*OpenAPISchema{
			"delete": {Type: "boolean"},
		},
	}

	for _, mod := range generator.Modules {
		generator.buildModulePaths(&spec, mod)
	}

	generator.buildFeaturesPath(&spec)
	generator.buildConfigPath(&spec)

	return spec
}

// ============================================
// Module path builder
// ============================================

func (generator *Generator) buildModulePaths(spec *OpenAPISpec, mod *BaseModule) {
	tag := mod.Name

	// Build a full record schema from all module fields
	recordSchemaName := "Record_" + mod.Name
	spec.Components.Schemas[recordSchemaName] = buildRecordSchema(mod.Fields, nil)

	for _, action := range mod.Actions {
		switch action.Action() {
		case actions.ModuleActionNameList:
			listAction, ok := action.(actions.ListModuleAction)
			if !ok {
				continue
			}
			generator.buildListPath(spec, mod, listAction, tag, recordSchemaName)

		case actions.ModuleActionNameAdd:
			addAction, ok := action.(actions.AddModuleAction)
			if !ok {
				continue
			}
			generator.buildAddPath(spec, mod, addAction, tag, recordSchemaName)
			generator.buildDefrecPath(spec, mod, tag)

		case actions.ModuleActionNameView:
			viewAction, ok := action.(actions.ViewModuleAction)
			if !ok {
				continue
			}
			generator.buildViewPath(spec, mod, viewAction, tag, recordSchemaName)

		case actions.ModuleActionNameUpdate:
			updateAction, ok := action.(actions.UpdateModuleAction)
			if !ok {
				continue
			}
			generator.buildUpdatePath(spec, mod, updateAction, tag, recordSchemaName)

		case actions.ModuleActionNameDelete:
			deleteAction, ok := action.(actions.DeleteModuleAction)
			if !ok {
				continue
			}
			generator.buildDeletePath(spec, mod, deleteAction, tag)
		}
	}
}

// ============================================
// LIST
// ============================================

func (generator *Generator) buildListPath(spec *OpenAPISpec, mod *BaseModule, action actions.ListModuleAction, tag, recordRef string) {
	path := mod.Path + "/" + mod.Name

	params := []OpenAPIParameter{
		{Name: "page", In: "query", Schema: &OpenAPISchema{Type: "integer"}, Description: "Page number (0-based)"},
		{Name: "size", In: "query", Schema: &OpenAPISchema{Type: "integer"}, Description: "Page size"},
		{Name: "search", In: "query", Schema: &OpenAPISchema{Type: "string"}, Description: "Search text"},
		{Name: "sort", In: "query", Schema: &OpenAPISchema{Type: "string"}, Description: "Sort field:direction (e.g. name:asc)"},
		{Name: "csv", In: "query", Schema: &OpenAPISchema{Type: "integer", Enum: []interface{}{0, 1}}, Description: "Export as CSV"},
		{Name: "addFilters", In: "query", Schema: &OpenAPISchema{Type: "string", Enum: []interface{}{"true"}}, Description: "Include filter metadata"},
		{Name: "addHeads", In: "query", Schema: &OpenAPISchema{Type: "string", Enum: []interface{}{"true"}}, Description: "Include column headers"},
		{Name: "lang", In: "query", Schema: &OpenAPISchema{Type: "string"}, Description: "Response language"},
	}

	// Add filter parameters for each filterable column
	for _, col := range action.Filter {
		params = append(params, OpenAPIParameter{
			Name:        fmt.Sprintf("filter[%s]", col.Name()),
			In:          "query",
			Schema:      &OpenAPISchema{Type: "string"},
			Description: fmt.Sprintf("Filter by %s", col.Name()),
		})
	}

	listResponseSchema := OpenAPISchema{
		Type: "object",
		Properties: map[string]*OpenAPISchema{
			"count":   {Type: "integer", Format: "int64"},
			"size":    {Type: "integer", Format: "int64"},
			"page":    {Type: "integer", Format: "int64"},
			"locale":  {Type: "string"},
			"locales": {Type: "array", Items: &OpenAPISchema{Type: "string"}},
			"rows":    {Type: "array", Items: &OpenAPISchema{Ref: "#/components/schemas/" + recordRef}},
			"heads":   {Type: "object"},
			"sort": {Type: "array", Items: &OpenAPISchema{
				Type: "object",
				Properties: map[string]*OpenAPISchema{
					"value": {Type: "string"},
					"text":  {Type: "string"},
				},
			}},
		},
	}
	listSchemaName := "ListResponse_" + mod.Name
	spec.Components.Schemas[listSchemaName] = listResponseSchema

	op := &OpenAPIOperation{
		Tags:        []string{tag},
		Summary:     fmt.Sprintf("List %s", mod.Name),
		OperationID: fmt.Sprintf("list_%s", mod.Name),
		Parameters:  params,
		Responses: map[string]OpenAPIResponse{
			"200": {
				Description: "Successful response",
				Content: map[string]OpenAPIMediaType{
					"application/json": {Schema: &OpenAPISchema{Ref: "#/components/schemas/" + listSchemaName}},
				},
			},
			"400": {
				Description: "Bad request",
				Content: map[string]OpenAPIMediaType{
					"application/json": {Schema: &OpenAPISchema{Ref: "#/components/schemas/ErrorResponse"}},
				},
			},
		},
	}

	applySecurity(op, action.Auth, action.Permission)

	pathItem := getOrCreatePathItem(spec, path)
	pathItem.Get = op
	spec.Paths[path] = pathItem
}

// ============================================
// ADD
// ============================================

func (generator *Generator) buildAddPath(spec *OpenAPISpec, mod *BaseModule, action actions.AddModuleAction, tag, recordRef string) {
	path := mod.Path + "/" + mod.Name

	addFields := filterFieldsByColumns(mod.Fields, action.Columns)
	scenario := fields.ScenarioAdd
	addSchemaName := "AddRequest_" + mod.Name
	spec.Components.Schemas[addSchemaName] = buildRecordSchema(addFields, &scenario)

	op := &OpenAPIOperation{
		Tags:        []string{tag},
		Summary:     fmt.Sprintf("Create %s", mod.Name),
		OperationID: fmt.Sprintf("add_%s", mod.Name),
		RequestBody: &OpenAPIRequestBody{
			Required: true,
			Content: map[string]OpenAPIMediaType{
				"application/json": {Schema: &OpenAPISchema{Ref: "#/components/schemas/" + addSchemaName}},
			},
		},
		Responses: map[string]OpenAPIResponse{
			"200": {
				Description: "Created record",
				Content: map[string]OpenAPIMediaType{
					"application/json": {Schema: &OpenAPISchema{
						Type: "object",
						Properties: map[string]*OpenAPISchema{
							"value":       {Type: "integer", Format: "int64"},
							"primary_key": {Type: "string"},
						},
					}},
				},
			},
			"400": {
				Description: "Validation error",
				Content: map[string]OpenAPIMediaType{
					"application/json": {Schema: &OpenAPISchema{Ref: "#/components/schemas/ErrorResponse"}},
				},
			},
		},
	}

	applySecurity(op, action.Auth, action.Permission)

	pathItem := getOrCreatePathItem(spec, path)
	pathItem.Put = op
	spec.Paths[path] = pathItem
}

// ============================================
// DEFREC
// ============================================

func (generator *Generator) buildDefrecPath(spec *OpenAPISpec, mod *BaseModule, tag string) {
	path := fmt.Sprintf("%s/%s/defrec/", mod.Path, mod.Name)

	op := &OpenAPIOperation{
		Tags:        []string{tag},
		Summary:     fmt.Sprintf("Get %s field metadata", mod.Name),
		OperationID: fmt.Sprintf("defrec_%s", mod.Name),
		Parameters: []OpenAPIParameter{
			{Name: "lang", In: "query", Schema: &OpenAPISchema{Type: "string"}, Description: "Response language"},
		},
		Responses: map[string]OpenAPIResponse{
			"200": {
				Description: "Field metadata",
				Content: map[string]OpenAPIMediaType{
					"application/json": {Schema: &OpenAPISchema{
						Type: "object",
						Properties: map[string]*OpenAPISchema{
							"extra":   {Type: "object"},
							"locale":  {Type: "string"},
							"locales": {Type: "array", Items: &OpenAPISchema{Type: "string"}},
							"fields":  {Type: "object"},
							"i18n":    {Type: "object"},
						},
					}},
				},
			},
		},
	}

	pathItem := getOrCreatePathItem(spec, path)
	pathItem.Get = op
	spec.Paths[path] = pathItem
}

// ============================================
// VIEW
// ============================================

func (generator *Generator) buildViewPath(spec *OpenAPISpec, mod *BaseModule, action actions.ViewModuleAction, tag, recordRef string) {
	path := fmt.Sprintf("%s/%s/view/{bykey}/{value}", mod.Path, mod.Name)

	op := &OpenAPIOperation{
		Tags:        []string{tag},
		Summary:     fmt.Sprintf("View %s", mod.Name),
		OperationID: fmt.Sprintf("view_%s", mod.Name),
		Parameters: []OpenAPIParameter{
			buildByKeyParam(action.By),
			{Name: "value", In: "path", Required: true, Schema: &OpenAPISchema{Type: "string"}, Description: "Lookup value"},
		},
		Responses: map[string]OpenAPIResponse{
			"200": {
				Description: "Record details",
				Content: map[string]OpenAPIMediaType{
					"application/json": {Schema: &OpenAPISchema{Ref: "#/components/schemas/" + recordRef}},
				},
			},
			"400": {
				Description: "Bad request",
				Content: map[string]OpenAPIMediaType{
					"application/json": {Schema: &OpenAPISchema{Ref: "#/components/schemas/ErrorResponse"}},
				},
			},
		},
	}

	applySecurity(op, action.Auth, action.Permission)

	pathItem := getOrCreatePathItem(spec, path)
	pathItem.Get = op
	spec.Paths[path] = pathItem
}

// ============================================
// UPDATE
// ============================================

func (generator *Generator) buildUpdatePath(spec *OpenAPISpec, mod *BaseModule, action actions.UpdateModuleAction, tag, recordRef string) {
	path := fmt.Sprintf("%s/%s/{bykey}/{value}", mod.Path, mod.Name)

	updateFields := filterFieldsByColumns(mod.Fields, action.Columns)
	scenario := fields.ScenarioUpdate
	updateSchemaName := "UpdateRequest_" + mod.Name
	spec.Components.Schemas[updateSchemaName] = buildRecordSchema(updateFields, &scenario)

	op := &OpenAPIOperation{
		Tags:        []string{tag},
		Summary:     fmt.Sprintf("Update %s", mod.Name),
		OperationID: fmt.Sprintf("update_%s", mod.Name),
		Parameters: []OpenAPIParameter{
			buildByKeyParam(action.By),
			{Name: "value", In: "path", Required: true, Schema: &OpenAPISchema{Type: "string"}, Description: "Lookup value"},
		},
		RequestBody: &OpenAPIRequestBody{
			Required: true,
			Content: map[string]OpenAPIMediaType{
				"application/json": {Schema: &OpenAPISchema{Ref: "#/components/schemas/" + updateSchemaName}},
			},
		},
		Responses: map[string]OpenAPIResponse{
			"200": {
				Description: "Updated record",
				Content: map[string]OpenAPIMediaType{
					"application/json": {Schema: &OpenAPISchema{Ref: "#/components/schemas/" + recordRef}},
				},
			},
			"400": {
				Description: "Validation error",
				Content: map[string]OpenAPIMediaType{
					"application/json": {Schema: &OpenAPISchema{Ref: "#/components/schemas/ErrorResponse"}},
				},
			},
		},
	}

	applySecurity(op, action.Auth, action.Permission)

	pathItem := getOrCreatePathItem(spec, path)
	pathItem.Post = op
	spec.Paths[path] = pathItem
}

// ============================================
// DELETE
// ============================================

func (generator *Generator) buildDeletePath(spec *OpenAPISpec, mod *BaseModule, action actions.DeleteModuleAction, tag string) {
	path := fmt.Sprintf("%s/%s/delete/{bykey}/{value}", mod.Path, mod.Name)

	op := &OpenAPIOperation{
		Tags:        []string{tag},
		Summary:     fmt.Sprintf("Delete %s", mod.Name),
		OperationID: fmt.Sprintf("delete_%s", mod.Name),
		Parameters: []OpenAPIParameter{
			buildByKeyParam(action.By),
			{Name: "value", In: "path", Required: true, Schema: &OpenAPISchema{Type: "string"}, Description: "Lookup value"},
		},
		Responses: map[string]OpenAPIResponse{
			"200": {
				Description: "Deleted",
				Content: map[string]OpenAPIMediaType{
					"application/json": {Schema: &OpenAPISchema{Ref: "#/components/schemas/DeleteResponse"}},
				},
			},
			"400": {
				Description: "Bad request",
				Content: map[string]OpenAPIMediaType{
					"application/json": {Schema: &OpenAPISchema{Ref: "#/components/schemas/ErrorResponse"}},
				},
			},
		},
	}

	applySecurity(op, action.Auth, action.Permission)

	pathItem := getOrCreatePathItem(spec, path)
	pathItem.Delete = op
	spec.Paths[path] = pathItem
}

// ============================================
// Helpers
// ============================================

func buildRecordSchema(moduleFields []fields.ModuleField, scenario *fields.Scenario) OpenAPISchema {
	schema := OpenAPISchema{
		Type:       "object",
		Properties: make(map[string]*OpenAPISchema),
	}

	for _, field := range moduleFields {
		prop := fieldToSchema(field)
		fieldKey := field.ColumnName()
		if field.Translatable {
			fieldKey = field.Name()
		}
		schema.Properties[fieldKey] = &prop

		if scenario != nil {
			for _, rule := range field.Check {
				intro, ok := rule.(fields.CheckRuleIntrospectable)
				if !ok {
					continue
				}
				info := intro.RuleInfo()
				if info.Type == "required" {
					for _, s := range info.Scenarios {
						if s == *scenario {
							schema.Required = append(schema.Required, fieldKey)
						}
					}
				}
			}
		}
	}

	return schema
}

func fieldToSchema(field fields.ModuleField) OpenAPISchema {
	if field.Translatable {
		return OpenAPISchema{
			Type:        "object",
			Description: "Translatable field: map of language code to value",
			Properties: map[string]*OpenAPISchema{
				"en": {Type: "string"},
				"ar": {Type: "string"},
			},
		}
	}

	s := OpenAPISchema{}

	switch field.Type {
	case fields.ModuleFieldTypeString:
		s.Type = "string"
	case fields.ModuleFieldTypeInt:
		s.Type = "integer"
		s.Format = "int64"
	case fields.ModuleFieldTypeFloat:
		s.Type = "number"
		s.Format = "double"
	case fields.ModuleFieldTypeArray:
		s.Type = "array"
		s.Items = &OpenAPISchema{Type: "string"}
	case fields.ModuleFieldTypeObject:
		s.Type = "object"
	default:
		s.Type = "string"
	}

	if field.Example != "" {
		s.Example = field.Example
	}

	for _, rule := range field.Check {
		intro, ok := rule.(fields.CheckRuleIntrospectable)
		if !ok {
			continue
		}
		info := intro.RuleInfo()
		switch info.Type {
		case "in":
			s.Enum = info.Values
		case "length":
			min := info.Min
			max := info.Max
			s.MinLength = &min
			s.MaxLength = &max
		case "url":
			s.Format = "uri"
		case "email":
			s.Format = "email"
		}
	}

	if len(field.Options) > 0 && s.Enum == nil {
		for _, opt := range field.Options {
			s.Enum = append(s.Enum, opt.Value)
		}
	}

	return s
}

func buildByKeyParam(byCols []pg.Column) OpenAPIParameter {
	allowed := make([]interface{}, len(byCols))
	for i, col := range byCols {
		allowed[i] = col.Name()
	}
	return OpenAPIParameter{
		Name:        "bykey",
		In:          "path",
		Required:    true,
		Description: "Column name to look up by",
		Schema: &OpenAPISchema{
			Type: "string",
			Enum: allowed,
		},
	}
}

func filterFieldsByColumns(moduleFields []fields.ModuleField, columns []pg.Column) []fields.ModuleField {
	if len(columns) == 0 {
		return moduleFields
	}
	var result []fields.ModuleField
	for _, f := range moduleFields {
		if fields.ContainsColumn(columns, f.Column) {
			result = append(result, f)
		}
	}
	return result
}

func applySecurity(op *OpenAPIOperation, auth bool, permissions []actions.Role) {
	if auth {
		op.Security = []map[string][]string{
			{"bearerAuth": {}},
		}
	}
	if len(permissions) > 0 {
		roles := make([]string, len(permissions))
		for i, r := range permissions {
			roles[i] = string(r)
		}
		if op.Description != "" {
			op.Description += ". "
		}
		op.Description += fmt.Sprintf("Requires roles: %s", strings.Join(roles, ", "))
	}
}

func getOrCreatePathItem(spec *OpenAPISpec, path string) OpenAPIPathItem {
	if existing, ok := spec.Paths[path]; ok {
		return existing
	}
	return OpenAPIPathItem{}
}

func (generator *Generator) buildFeaturesPath(spec *OpenAPISpec) {
	path := "/api/features"
	spec.Components.Schemas["FeaturesResponse"] = OpenAPISchema{
		Type: "object",
		Properties: map[string]*OpenAPISchema{
			"modules": {
				Type: "array",
				Items: &OpenAPISchema{
					Type: "object",
					Properties: map[string]*OpenAPISchema{
						"module_name": {Type: "string"},
						"actions": {Type: "object"},
					},
				},
			},
		},
	}
	op := &OpenAPIOperation{
		Tags:        []string{"system"},
		Summary:     "Get available modules and actions",
		OperationID: "get_features",
		Responses: map[string]OpenAPIResponse{
			"200": {
				Description: "List of modules with actions",
				Content: map[string]OpenAPIMediaType{
					"application/json": {Schema: &OpenAPISchema{Ref: "#/components/schemas/FeaturesResponse"}},
				},
			},
		},
	}
	pathItem := getOrCreatePathItem(spec, path)
	pathItem.Get = op
	spec.Paths[path] = pathItem
}

func (generator *Generator) buildConfigPath(spec *OpenAPISpec) {
	path := "/api/config"
	spec.Components.Schemas["ConfigResponse"] = OpenAPISchema{
		Type: "object",
		Properties: map[string]*OpenAPISchema{
			"left_menu": {Type: "array", Items: &OpenAPISchema{Type: "object"}},
			"routes": {Type: "object"},
			"role": {Type: "string"},
		},
	}
	op := &OpenAPIOperation{
		Tags:        []string{"system"},
		Summary:     "Get configuration for webapp",
		OperationID: "get_config",
		Security:    []map[string][]string{{"bearerAuth": {}}},
		Responses: map[string]OpenAPIResponse{
			"200": {
				Description: "Configuration object",
				Content: map[string]OpenAPIMediaType{
					"application/json": {Schema: &OpenAPISchema{Ref: "#/components/schemas/ConfigResponse"}},
				},
			},
			"401": {
				Description: "Unauthorized",
				Content: map[string]OpenAPIMediaType{
					"application/json": {Schema: &OpenAPISchema{Ref: "#/components/schemas/ErrorResponse"}},
				},
			},
		},
	}
	pathItem := getOrCreatePathItem(spec, path)
	pathItem.Get = op
	spec.Paths[path] = pathItem
}
