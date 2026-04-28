package module

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/darkrain/request-generator/actions"
	"github.com/darkrain/request-generator/db"
	"github.com/darkrain/request-generator/fields"
	"github.com/darkrain/request-generator/icontext"
	"github.com/darkrain/request-generator/locale"
	"github.com/darkrain/request-generator/response"
	"github.com/darkrain/request-generator/utils"
	"github.com/gin-gonic/gin"
	pg "github.com/go-jet/jet/v2/postgres"
	validation "github.com/go-ozzo/ozzo-validation/v4"
)

const (
	GeneratorErrorAdd    string = "Cannot create record"
	GeneratorErrorUpdate string = "Cannot update record"
	GeneratorErrorDelete string = "Cannot delete record"
)

type Generator struct {
	db                   func(module *BaseModule) db.DBExecutor
	group                gin.RouterGroup
	Modules              []*BaseModule
	Features             []Features
	AuthMiddleware       func(module actions.ModuleAction) gin.HandlerFunc
	PermissionMiddleware func(action actions.ModuleAction, permissions []actions.Role) gin.HandlerFunc
	Locales              []locale.Lang
	DefaultLocale        locale.Lang
	translations         map[locale.Lang]map[string]string
	EnableOpenAPI        bool
	GroupTitles          map[string]string
	ViewAdapters         map[string]string
	IconMap              map[string]string
	// Global hooks for all actions
	GlobalBeforeAction   func(c *gin.Context, module *BaseModule, action actions.ModuleAction) error
	GlobalAfterAction    func(c *gin.Context, module *BaseModule, action actions.ModuleAction)
}

func NewGenerator(
	db func(module *BaseModule) db.DBExecutor,
	group gin.RouterGroup,
	modules []*BaseModule,
	permissionMiddleware func(action actions.ModuleAction, permissions []actions.Role) gin.HandlerFunc,
	authMiddleware func(action actions.ModuleAction) gin.HandlerFunc,
) *Generator {
	return &Generator{
		db:                   db,
		group:                group,
		Modules:              modules,
		Features:             []Features{},
		PermissionMiddleware: permissionMiddleware,
		AuthMiddleware:       authMiddleware,
		Locales:              []locale.Lang{locale.EN},
		DefaultLocale:        locale.EN,
	}
}

func (generator *Generator) getLang(c *gin.Context) locale.Lang {
	if lang := c.Query("lang"); lang != "" {
		l := locale.Lang(lang)
		for _, s := range generator.Locales {
			if s == l {
				return l
			}
		}
	}
	return locale.ParseAcceptLanguage(c.GetHeader("Accept-Language"), generator.Locales, generator.DefaultLocale)
}

func (generator *Generator) FeaturesMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		l, _ := icontext.GetLogger(ctx)
		lang := generator.getLang(c)

		localized := make([]Features, len(generator.Features))
		for i, f := range generator.Features {
			lf := Features{
				ModuleName: generator.Translate(lang, f.ModuleName),
				Actions:    make(map[string]FeaturesActions, len(f.Actions)),
			}
			for k, a := range f.Actions {
				lf.Actions[k] = FeaturesActions{
					Label: generator.Translate(lang, a.Label),
					Url:   a.Url,
					Type:  a.Type,
					Roles: a.Roles,
				}
			}
			localized[i] = lf
		}

		resp := FeaturesResponse{
			Modules: localized,
		}

		response.Response(l, c, resp)
	}
}

func (generator *Generator) Run() {

	featuresGroup := generator.group.Group("/api")
	featuresGroup.GET("/features", generator.FeaturesMiddleware())

	for _, module := range generator.Modules {
		featuresModule := Features{
			ModuleName:       module.Label,
			ModuleNameLabels: module.Labels,
			Actions:          make(map[string]FeaturesActions),
		}

		for _, action := range module.Actions {
			switch action.Action() {
			case actions.ModuleActionNameList:

				listAction, _ := action.(actions.ListModuleAction)
				featuresModule.Actions["list"] = FeaturesActions{
					Label:  listAction.Label,
					Labels: listAction.Labels,
					Url:    module.Path + "/" + module.Name,
					Type:   "GET",
					Roles:  listAction.Permission,
				}
				listGrpup := generator.group.Group(module.Path)
				if listAction.Auth {
					if generator.AuthMiddleware == nil {
						panic(fmt.Sprintf("auth middleware not implemented in module: %s", module.Name))
					}
					listGrpup.Use(generator.AuthMiddleware(listAction))
				}
				if len(listAction.Permission) > 0 {
					if generator.PermissionMiddleware == nil {
						panic(fmt.Sprintf("permission middleware not implemented in module: %s", module.Name))
					}
					listGrpup.Use(generator.PermissionMiddleware(listAction, listAction.Permission))
				}

				listGrpup.GET(module.Name, generator.actionList(module, listAction))
			case actions.ModuleActionNameAdd:
				addAction, _ := action.(actions.AddModuleAction)
				featuresModule.Actions["add"] = FeaturesActions{
					Label:  addAction.Label,
					Labels: addAction.Labels,
					Url:    module.Path + "/" + module.Name,
					Type:   "PUT",
					Roles:  addAction.Permission,
				}
				featuresModule.Actions["defrec"] = FeaturesActions{
					Label:  addAction.Label,
					Labels: addAction.Labels,
					Url:    fmt.Sprintf("%s/%s/defrec/", module.Path, module.Name),
					Type:   "GET",
					Roles:  addAction.Permission,
				}
				addGrpup := generator.group.Group(module.Path)
				if addAction.Auth {
					if generator.AuthMiddleware == nil {
						panic(fmt.Sprintf("auth middleware not implemented in module: %s", module.Name))
					}
					addGrpup.Use(generator.AuthMiddleware(addAction))
				}
				if len(addAction.Permission) > 0 {
					if generator.PermissionMiddleware == nil {
						panic(fmt.Sprintf("permission middleware not implemented in module: %s", module.Name))
					}
					addGrpup.Use(generator.PermissionMiddleware(addAction, addAction.Permission))
				}
				addGrpup.PUT(module.Name, generator.actionAdd(module, addAction))

				defrecGroup := generator.group.Group(fmt.Sprintf("%s/%s/defrec", module.Path, module.Name))
				defrecGroup.GET("/", generator.actionDefrec(module))

			case actions.ModuleActionNameView:
				viewAction, _ := action.(actions.ViewModuleAction)
				featuresModule.Actions["view"] = FeaturesActions{
					Label:  viewAction.Label,
					Labels: viewAction.Labels,
					Url:    module.Path + "/" + module.Name,
					Type:   "GET",
					Roles:  viewAction.Permission,
				}
				viewGrout := generator.group.Group(module.Path)
				if viewAction.Auth {
					if generator.AuthMiddleware == nil {
						panic(fmt.Sprintf("auth middleware not implemented in module: %s", module.Name))
					}
					viewGrout.Use(generator.AuthMiddleware(viewAction))
				}
				if len(viewAction.Permission) > 0 {
					if generator.PermissionMiddleware == nil {
						panic(fmt.Sprintf("permission middleware not implemented in module: %s", module.Name))
					}
					viewGrout.Use(generator.PermissionMiddleware(viewAction, viewAction.Permission))
				}

				viewGrout.GET(fmt.Sprintf("%s/view/:bykey/:value", module.Name), generator.actionView(module, viewAction))
			case actions.ModuleActionNameUpdate:
				updateAction, _ := action.(actions.UpdateModuleAction)
				featuresModule.Actions["update"] = FeaturesActions{
					Label:  updateAction.Label,
					Labels: updateAction.Labels,
					Url:    module.Path + "/" + module.Name,
					Type:   "POST",
					Roles:  updateAction.Permission,
				}
				updateGroup := generator.group.Group(module.Path)
				if updateAction.Auth {
					if generator.AuthMiddleware == nil {
						panic(fmt.Sprintf("auth middleware not implemented in module: %s", module.Name))
					}
					updateGroup.Use(generator.AuthMiddleware(updateAction))
				}
				if len(updateAction.Permission) > 0 {
					if generator.PermissionMiddleware == nil {
						panic(fmt.Sprintf("permission middleware not implemented in module: %s", module.Name))
					}
					updateGroup.Use(generator.PermissionMiddleware(updateAction, updateAction.Permission))
				}

				updateGroup.POST(fmt.Sprintf("%s/:bykey/:value", module.Name), generator.actionUpdate(module, updateAction))
			case actions.ModuleActionNameDelete:
				deleteAction, _ := action.(actions.DeleteModuleAction)
				featuresModule.Actions["delete"] = FeaturesActions{
					Label:  deleteAction.Label,
					Labels: deleteAction.Labels,
					Url:    module.Path + "/" + module.Name,
					Type:   "DELETE",
					Roles:  deleteAction.Permission,
				}
				deleteGroup := generator.group.Group(module.Path)
				if deleteAction.Auth {
					if generator.AuthMiddleware == nil {
						panic(fmt.Sprintf("auth middleware not implemented in module: %s", module.Name))
					}
					deleteGroup.Use(generator.AuthMiddleware(deleteAction))
				}
				if len(deleteAction.Permission) > 0 {
					if generator.PermissionMiddleware == nil {
						panic(fmt.Sprintf("permission middleware not implemented in module: %s", module.Name))
					}
					deleteGroup.Use(generator.PermissionMiddleware(deleteAction, deleteAction.Permission))
				}
				deleteGroup.DELETE(fmt.Sprintf("%s/delete/:bykey/:value", module.Name), generator.actionDelete(module, deleteAction))
			}
		}

		generator.Features = append(generator.Features, featuresModule)
	}

	// Locale endpoints
	featuresGroup.GET("/lang", generator.handleLangList())
	featuresGroup.GET("/lang/:key", generator.handleLangTranslations())

	// Config endpoint (protected by AuthMiddleware)
	if generator.AuthMiddleware != nil {
		configGroup := featuresGroup.Group("")
		// Dummy action for auth middleware (config doesn't map to a specific module action)
		dummyAction := actions.ListModuleAction{
			Permission: []actions.Role{},
			Auth:       true,
		}
		configGroup.Use(generator.AuthMiddleware(dummyAction))
		configGroup.GET("/config", generator.actionConfigEndpoint())
	}

	// Build and serve OpenAPI 3.0 spec (only when enabled)
	if generator.EnableOpenAPI {
		spec := generator.buildOpenAPISpec("theGHub1 API", "1.0")
		specJSON, err := json.MarshalIndent(spec, "", "  ")
		if err != nil {
			panic(fmt.Sprintf("failed to marshal OpenAPI spec: %v", err))
		}

		featuresGroup.GET("/openapi.json", func(c *gin.Context) {
			c.Data(http.StatusOK, "application/json; charset=utf-8", specJSON)
		})
	}
}

func (generator *Generator) actionList(module *BaseModule, action actions.ListModuleAction) func(c *gin.Context) {
	return func(c *gin.Context) {
		defer action.AfterRequest(c)

		ctx := c.Request.Context()
		l, _ := icontext.GetLogger(ctx)
		role := actions.GetRoleFromContext(c)
		lang := generator.getLang(c)

		if hook := actions.ResolveRoleHook(module.RoleBeforeHook, role); hook != nil {
			if err := hook(c); err != nil {
				response.ErrorResponse(l, c, http.StatusBadRequest, err.Error(), nil)
				return
			}
		}
		defer func() {
			if hook := actions.ResolveRoleAfterHook(module.RoleAfterHook, role); hook != nil {
				hook(c)
			}
		}()

		err := action.BeforeRequest(c)
		if err != nil {
			response.ErrorResponse(l, c, http.StatusBadRequest, err.Error(), nil)
			return
		}

		page := int64QueryParam(c, "page", 0)
		defaultSize := int64(3000)
		if action.Size > 0 {
			defaultSize = action.Size
		}
		size := int64QueryParam(c, "size", defaultSize)
		if action.Maxsize > 0 && size > action.Maxsize {
			size = action.Maxsize
		}
		isCSV := int64QueryParam(c, "csv", 0)
		filters := generator.normalizeFilters(c.QueryMap("filter"), module, action, lang)
		searchText := c.Query("search")
		addFilters := c.Query("addFilters")
		addHeads := c.Query("addHeads")

		columns := action.GetColumns(c)

		realFields := make([]fields.ModuleField, 0, 10)
		for _, realField := range module.Fields {
			if containsColumn(columns, realField.Column) {
				realFields = append(realFields, realField)
			}
		}

		var where pg.BoolExpression
		if whereFn := actions.ResolveRoleWhere(module.RoleWhere, role); whereFn != nil {
			where = whereFn(c)
		}
		if action.Where != nil {
			actionWhere := action.Where(c)
			if where != nil {
				where = pg.AND(where, actionWhere)
			} else {
				where = actionWhere
			}
		}

		joins := action.Join
		if roleJoins := actions.ResolveRoleJoin(module.RoleJoin, role); roleJoins != nil {
			joins = append(roleJoins, joins...)
		}

		var activeSort *actions.SortOption
		if sortParam := c.Query("sort"); sortParam != "" && len(action.Sort) > 0 {
			parts := strings.SplitN(sortParam, ":", 2)
			colName := parts[0]
			dir := actions.SortASC
			if len(parts) == 2 && parts[1] == "desc" {
				dir = actions.SortDESC
			}
			for _, col := range action.Sort {
				if col.Name() == colName {
					activeSort = &actions.SortOption{Column: col, Direction: dir}
					break
				}
			}
		} else if action.SortDefault != nil {
			activeSort = &actions.SortOption{Column: action.SortDefault, Direction: actions.SortASC}
		}

		tc := generator.buildTranslationContext(module)

		results, count, err := generator.db(module).List(
			l,
			module.Table,
			module.PrimaryKey,
			realFields,
			page,
			size,
			action.Search,
			searchText,
			filters,
			where,
			joins,
			activeSort,
			tc,
		)

		if err != nil {
			response.ErrorResponse(l, c, http.StatusBadRequest, err.Error(), nil)
			return
		}

		var heads map[string]interface{}
		if addHeads == "true" {
			heads = make(map[string]interface{})

			for _, realField := range module.Fields {
				if containsColumn(columns, realField.Column) {
					headItem := map[string]interface{}{
						"title": generator.Translate(lang, realField.Title),
					}
					if realField.Extra != nil && realField.Extra.List != nil {
						headItem["extra"] = realField.Extra.List
					}
					heads[realField.ColumnName()] = headItem
				}
			}
		}

		var filter map[string]fields.ModuleFilterField
		if addFilters == "true" {
			filter = make(map[string]fields.ModuleFilterField)
			for _, realField := range module.Fields {
				if containsColumn(action.Filter, realField.Column) {
					options := make([]fields.ModuleFieldOptions, 0, 10)
					if realField.Options != nil {
						for _, item := range realField.Options {
							options = append(options, item)
						}
					}
					if realField.OptionsFunc != nil {
						for _, item := range realField.OptionsFunc(c) {
							options = append(options, item)
						}
					}
					roleStr := string(actions.GetRoleFromContext(c))
					for _, ro := range realField.RoleOptions {
						if ro.Role == roleStr || ro.Role == string(actions.RoleAll) {
							options = append(options, ro.Options...)
							break
						}
					}

					for i, opt := range options {
						options[i].Label = generator.Translate(lang, opt.Label)
					}

					filterField := fields.ModuleFilterField{
						Column:   realField.Column,
						Title:    generator.Translate(lang, realField.Title),
						Type:     realField.Type,
						FormType: realField.FormType,
						Example:  realField.Example,
						Options:  options,
						Check:    realField.Check,
						Convert:  realField.Convert,
					}
					filter[realField.ColumnName()] = filterField
				}
			}
		}

		if len(results) == 0 {
			results = make([]interface{}, 0, 10)
		}

		if len(heads) == 0 {
			heads = make(map[string]interface{})
		}

		var sortOptions []actions.SortResponseItem
		if len(action.Sort) > 0 {
			for _, col := range action.Sort {
				field := module.GetFieldByColumn(col)
				label := col.Name()
				if field != nil {
					label = generator.Translate(lang, field.Title)
				}
				sortOptions = append(sortOptions,
					actions.SortResponseItem{Value: col.Name() + ":asc", Text: label + " ↑"},
					actions.SortResponseItem{Value: col.Name() + ":desc", Text: label + " ↓"},
				)
			}
		}

		output := struct {
			Count   int64                               `json:"count"`
			Size    int64                               `json:"size"`
			Page    int64                               `json:"page"`
			Extra   interface{}                         `json:"extra"`
			Rows    []interface{}                       `json:"rows"`
			Heads   map[string]interface{}               `json:"heads"`
			Filters map[string]fields.ModuleFilterField `json:"filters,omitempty"`
			Sort    []actions.SortResponseItem          `json:"sort,omitempty"`
		}{
			Count:   count,
			Size:    size,
			Page:    page,
			Extra:   action.Extra,
			Rows:    results,
			Heads:   heads,
			Filters: filter,
			Sort:      sortOptions,
		}

		if isCSV == 0 {
			response.Response(l, c, output)
		} else {
			resultJsonString, err := json.Marshal(results)
			if err != nil {
				response.ErrorResponse(l, c, http.StatusInternalServerError, err.Error(), nil)
				return
			}

			var d []map[string]interface{}
			err = json.Unmarshal(resultJsonString, &d)
			if err != nil {
				response.ErrorResponse(l, c, http.StatusInternalServerError, err.Error(), nil)
				return
			}

			csvResults := make([][]string, 0, 10)
			keys := make([]string, 0, 10)
			for _, v := range d {
				for key := range v {
					keys = append(keys, key)
				}
				break
			}
			sort.Strings(keys)
			csvResults = append(csvResults, keys)

			for _, v := range d {
				values := make([]string, 0, 10)
				for _, key := range keys {
					valueString, err := json.Marshal(v[key])
					if err != nil {
						continue
					}

					values = append(values, string(valueString))
				}
				csvResults = append(csvResults, values)
			}

			b := new(bytes.Buffer)
			w := csv.NewWriter(b)
			w.Comma = '\t'
			err = w.WriteAll(csvResults)

			response.ResponseCSV(l, c, b.Bytes())
		}
	}
}

func (generator *Generator) actionAdd(module *BaseModule, action actions.AddModuleAction) func(c *gin.Context) {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		l, _ := icontext.GetLogger(ctx)
		role := actions.GetRoleFromContext(c)
		lang := generator.getLang(c)

		if hook := actions.ResolveRoleHook(module.RoleBeforeHook, role); hook != nil {
			if err := hook(c); err != nil {
				response.ErrorResponse(l, c, http.StatusBadRequest, err.Error(), nil)
				return
			}
		}
		defer func() {
			if hook := actions.ResolveRoleAfterHook(module.RoleAfterHook, role); hook != nil {
				hook(c)
			}
		}()

		// Global before hook
		if generator.GlobalBeforeAction != nil {
			if err := generator.GlobalBeforeAction(c, module, action); err != nil {
				response.ErrorResponse(l, c, http.StatusBadRequest, GeneratorErrorAdd, []string{
					err.Error(),
				})
				return
			}
		}

		err := action.BeforeRequest(c)
		if err != nil {
			response.ErrorResponse(l, c, http.StatusBadRequest, GeneratorErrorAdd, []string{
				err.Error(),
			})
			return
		}

		var input map[string]interface{}
		err = utils.ParseJson(c.Request, &input)
		if err != nil {
			response.ErrorResponse(l, c, http.StatusBadRequest, GeneratorErrorAdd, []string{
				"Parse Input Error",
			})
			return
		}

		errs := generator.checkRequest(c, input, module, action, fields.ScenarioAdd, lang)
		if len(errs) > 0 {
			response.ErrorResponse(l, c, http.StatusBadRequest, GeneratorErrorAdd, errs)
			return
		}

		columns := action.GetColumns(c)

		realFields := make([]fields.ModuleField, 0, 10)
		for _, realField := range module.Fields {
			if containsColumn(columns, realField.Column) {
				realFields = append(realFields, realField)
			}
		}

		tc := generator.buildTranslationContext(module)

		mapInput := generator.mapRequestInput(input, module, columns)
		output, err := generator.db(module).Add(l, module.Table, module.PrimaryKey, realFields, mapInput, tc)
		if err != nil {
			response.ErrorResponse(l, c, http.StatusBadRequest, GeneratorErrorAdd, []string{
				err.Error(),
			})
			return
		}

		response.Response(l, c, output)

		action.AfterRequest(c)
	}
}

func (generator *Generator) actionDefrec(module *BaseModule) func(c *gin.Context) {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		l, _ := icontext.GetLogger(ctx)
		lang := generator.getLang(c)

		err := module.Defrec.BeforeRequest(c)
		if err != nil {
			response.ErrorResponse(l, c, http.StatusBadRequest, err.Error(), nil)
			return
		}

		output := make([]fields.ModuleField, 0, 10)

		role := string(actions.GetRoleFromContext(c))

		for _, field := range module.Fields {
			checkItems := make([]fields.CheckRules, 0, 10)
			optionItems := make([]fields.ModuleFieldOptions, 0, 10)

			if field.Options != nil {
				for _, option := range field.Options {
					optionItems = append(optionItems, option)
				}
			}
			if field.OptionsFunc != nil {
				for _, option := range field.OptionsFunc(c) {
					optionItems = append(optionItems, option)
				}
			}
			for _, ro := range field.RoleOptions {
				if ro.Role == role || ro.Role == string(actions.RoleAll) {
					optionItems = append(optionItems, ro.Options...)
					break
				}
			}

			for i, opt := range optionItems {
				optionItems[i].Label = generator.Translate(lang, opt.Label)
			}

			if field.Check != nil {
				for _, check := range field.Check {
					checkItems = append(checkItems, check)
				}
			}
			if field.CheckFunc != nil {
				for _, check := range field.CheckFunc(c) {
					checkItems = append(checkItems, check)
				}
			}
			for _, rc := range field.RoleCheck {
				if rc.Role == role || rc.Role == string(actions.RoleAll) {
					checkItems = append(checkItems, rc.Rules...)
					break
				}
			}

			field.Title = generator.Translate(lang, field.Title)
			field.Options = optionItems
			field.Check = checkItems

			output = append(output, field)
		}

		response.Response(l, c, response.NewDefrecResponse(nil, output))

		module.Defrec.AfterRequest(c)
	}
}

func (generator *Generator) actionView(module *BaseModule, action actions.ViewModuleAction) func(c *gin.Context) {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		l, _ := icontext.GetLogger(ctx)
		role := actions.GetRoleFromContext(c)

		if hook := actions.ResolveRoleHook(module.RoleBeforeHook, role); hook != nil {
			if err := hook(c); err != nil {
				response.ErrorResponse(l, c, http.StatusBadRequest, err.Error(), nil)
				return
			}
		}
		defer func() {
			if hook := actions.ResolveRoleAfterHook(module.RoleAfterHook, role); hook != nil {
				hook(c)
			}
		}()

		err := action.BeforeRequest(c)
		if err != nil {
			response.ErrorResponse(l, c, http.StatusBadRequest, err.Error(), nil)
			return
		}

		whereKey := c.Param("bykey")

		allowedKeys := make([]interface{}, 0, len(action.By))
		for _, col := range action.By {
			allowedKeys = append(allowedKeys, col.Name())
		}
		err = validation.In(allowedKeys...).Error(fmt.Sprintf(`allowed keys %v`, allowedKeys)).Validate(whereKey)
		if err != nil {
			response.ErrorResponse(l, c, http.StatusBadRequest, GeneratorErrorDelete, []string{
				err.Error(),
			})
			return
		}

		whereValue := c.Param("value")
		if len(whereValue) == 0 {
			response.ErrorResponse(l, c, http.StatusBadRequest, GeneratorErrorDelete, []string{
				"value param not found",
			})
			return
		}

		columns := action.GetColumns(c)

		realFields := make([]fields.ModuleField, 0, 10)
		for _, realField := range module.Fields {
			if containsColumn(columns, realField.Column) {
				realFields = append(realFields, realField)
			}
		}

		where := pg.RawBool(
			fmt.Sprintf(`%s."%s" = #val`, module.Table.Alias(), whereKey),
			pg.RawArgs{"#val": whereValue},
		)
		if module.Table.Alias() == "" {
			where = pg.RawBool(
				fmt.Sprintf(`"%s"."%s" = #val`, module.Table.TableName(), whereKey),
				pg.RawArgs{"#val": whereValue},
			)
		}

		joins := action.Join
		if roleJoins := actions.ResolveRoleJoin(module.RoleJoin, role); roleJoins != nil {
			joins = append(roleJoins, joins...)
		}

		tc := generator.buildTranslationContext(module)

		result, err := generator.db(module).View(l, module.Table, module.PrimaryKey, realFields, where, joins, tc)
		if err != nil {
			response.ErrorResponse(l, c, http.StatusBadRequest, err.Error(), nil)
			return
		}

		// Build rich view response with field metadata
		resultMap, ok := result.(map[string]interface{})
		if !ok {
			response.Response(l, c, result)
			action.AfterRequest(c)
			return
		}

		// Determine editable columns from UpdateAction
		var editableColumns []pg.Column
		if updateAction := findUpdateAction(module); updateAction != nil {
			editableColumns = updateAction.GetColumns(c)
		}

		lang := generator.getLang(c)
		roleStr := string(role)

		item := make(map[string]interface{}, len(realFields))
		for _, field := range realFields {
			fieldKey := field.ColumnName()
			if field.Translatable {
				fieldKey = field.Name()
			}
			value := resultMap[fieldKey]

			fieldItem := map[string]interface{}{
				"title":     generator.Translate(lang, field.Title),
				"type":      string(field.Type),
				"form_type": string(field.FormType),
				"value":     value,
				"edit":      containsColumn(editableColumns, field.Column),
			}

			if field.Extra != nil && field.Extra.View != nil {
				fieldItem["extra"] = field.Extra.View
			}

			// Collect options
			options := make([]fields.ModuleFieldOptions, 0)
			if field.Options != nil {
				options = append(options, field.Options...)
			}
			if field.OptionsFunc != nil {
				options = append(options, field.OptionsFunc(c)...)
			}
			for _, ro := range field.RoleOptions {
				if ro.Role == roleStr || ro.Role == string(actions.RoleAll) {
					options = append(options, ro.Options...)
					break
				}
			}
			if len(options) > 0 {
				for i, opt := range options {
					options[i].Label = generator.Translate(lang, opt.Label)
				}
				fieldItem["options"] = options
			}

			item[fieldKey] = fieldItem
		}

		output := struct {
			Extra interface{}            `json:"extra"`
			Item  map[string]interface{} `json:"item"`
		}{
			Extra: action.Extra,
			Item:  item,
		}

		response.Response(l, c, output)

		action.AfterRequest(c)
	}
}

func (generator *Generator) actionUpdate(module *BaseModule, action actions.UpdateModuleAction) func(c *gin.Context) {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		l, _ := icontext.GetLogger(ctx)
		role := actions.GetRoleFromContext(c)
		lang := generator.getLang(c)

		if hook := actions.ResolveRoleHook(module.RoleBeforeHook, role); hook != nil {
			if err := hook(c); err != nil {
				response.ErrorResponse(l, c, http.StatusBadRequest, err.Error(), nil)
				return
			}
		}
		defer func() {
			if hook := actions.ResolveRoleAfterHook(module.RoleAfterHook, role); hook != nil {
				hook(c)
			}
		}()

		// Global before hook
		if generator.GlobalBeforeAction != nil {
			if err := generator.GlobalBeforeAction(c, module, action); err != nil {
				response.ErrorResponse(l, c, http.StatusBadRequest, GeneratorErrorUpdate, nil)
				return
			}
		}

		err := action.BeforeRequest(c)
		if err != nil {
			response.ErrorResponse(l, c, http.StatusBadRequest, GeneratorErrorUpdate, nil)
			return
		}

		whereKey := c.Param("bykey")
		allowedKeys := make([]interface{}, 0, len(action.By))
		for _, col := range action.By {
			allowedKeys = append(allowedKeys, col.Name())
		}
		err = validation.In(allowedKeys...).Error(fmt.Sprintf(`allowed keys %v`, allowedKeys)).Validate(whereKey)
		if err != nil {
			response.ErrorResponse(l, c, http.StatusBadRequest, GeneratorErrorUpdate, []string{
				err.Error(),
			})
			return
		}

		whereValue := c.Param("value")
		if len(whereValue) == 0 {
			response.ErrorResponse(l, c, http.StatusBadRequest, GeneratorErrorUpdate, []string{
				"value param not found",
			})
			return
		}

		var input map[string]interface{}
		err = utils.ParseJson(c.Request, &input)
		if err != nil {
			response.ErrorResponse(l, c, http.StatusBadRequest, GeneratorErrorUpdate, nil)
			return
		}

		errs := generator.checkRequest(c, input, module, action, fields.ScenarioUpdate, lang)
		if len(errs) > 0 {
			response.ErrorResponse(l, c, http.StatusBadRequest, GeneratorErrorUpdate, errs)
			return
		}

		columns := action.GetColumns(c)

		realFields := make([]fields.ModuleField, 0, 10)
		for _, realField := range module.Fields {
			if containsColumn(columns, realField.Column) {
				realFields = append(realFields, realField)
			}
		}

		tc := generator.buildTranslationContext(module)
		if tc != nil {
			tc.EntityID = whereValue
		}

		mapInput := generator.mapRequestInput(input, module, columns)

		// Build WHERE condition
		where := pg.RawBool(
			fmt.Sprintf(`"%s" = #val`, whereKey),
			pg.RawArgs{"#val": whereValue},
		)

		_, err = generator.db(module).Update(l, module.Table, module.PrimaryKey, realFields, mapInput, where, tc)
		if err != nil {
			response.ErrorResponse(l, c, http.StatusBadRequest, GeneratorErrorUpdate, nil)
			return
		}

		// Return view response if ViewAfterUpdate is enabled (default true) and ViewAction exists
		useView := action.ViewAfterUpdate == nil || *action.ViewAfterUpdate
		if useView {
			if viewAction := findViewAction(module); viewAction != nil {
				viewColumns := viewAction.GetColumns(c)
				viewFields := make([]fields.ModuleField, 0, len(module.Fields))
				for _, f := range module.Fields {
					if containsColumn(viewColumns, f.Column) {
						viewFields = append(viewFields, f)
					}
				}

				viewJoins := viewAction.Join
				if roleJoins := actions.ResolveRoleJoin(module.RoleJoin, role); roleJoins != nil {
					viewJoins = append(roleJoins, viewJoins...)
				}

				viewResult, viewErr := generator.db(module).View(l, module.Table, module.PrimaryKey, viewFields, where, viewJoins, tc)
				if viewErr == nil {
					response.Response(l, c, viewResult)
					action.AfterRequest(c)
					return
				}
			}
		}

		// Fallback: re-fetch with update columns
		fallbackResult, fallbackErr := generator.db(module).View(l, module.Table, module.PrimaryKey, realFields, where, nil, tc)
		if fallbackErr != nil {
			response.ErrorResponse(l, c, http.StatusBadRequest, GeneratorErrorUpdate, nil)
			return
		}

		response.Response(l, c, fallbackResult)

		action.AfterRequest(c)

		// Global after hook
		if generator.GlobalAfterAction != nil {
			generator.GlobalAfterAction(c, module, action)
		}
	}
}

func (generator *Generator) actionDelete(module *BaseModule, action actions.DeleteModuleAction) func(c *gin.Context) {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		l, _ := icontext.GetLogger(ctx)
		role := actions.GetRoleFromContext(c)

		if hook := actions.ResolveRoleHook(module.RoleBeforeHook, role); hook != nil {
			if err := hook(c); err != nil {
				response.ErrorResponse(l, c, http.StatusBadRequest, err.Error(), nil)
				return
			}
		}
		defer func() {
			if hook := actions.ResolveRoleAfterHook(module.RoleAfterHook, role); hook != nil {
				hook(c)
			}
		}()

		// Global before hook
		if generator.GlobalBeforeAction != nil {
			if err := generator.GlobalBeforeAction(c, module, action); err != nil {
				response.ErrorResponse(l, c, http.StatusBadRequest, GeneratorErrorDelete, nil)
				return
			}
		}

		err := action.BeforeRequest(c)
		if err != nil {
			response.ErrorResponse(l, c, http.StatusBadRequest, GeneratorErrorDelete, nil)
			return
		}

		whereKey := c.Param("bykey")
		allowedKeys := make([]interface{}, 0, len(action.By))
		for _, col := range action.By {
			allowedKeys = append(allowedKeys, col.Name())
		}
		err = validation.In(allowedKeys...).Error(fmt.Sprintf(`allowed keys %v`, allowedKeys)).Validate(whereKey)
		if err != nil {
			response.ErrorResponse(l, c, http.StatusBadRequest, GeneratorErrorDelete, []string{
				err.Error(),
			})
			return
		}

		whereValue := c.Param("value")
		if len(whereValue) == 0 {
			response.ErrorResponse(l, c, http.StatusBadRequest, GeneratorErrorDelete, nil)
			return
		}

		tc := generator.buildTranslationContext(module)
		if tc != nil {
			tc.EntityID = whereValue
		}

		// Build WHERE condition
		where := pg.RawBool(
			fmt.Sprintf(`"%s" = #val`, whereKey),
			pg.RawArgs{"#val": whereValue},
		)

		err = generator.db(module).Delete(l, module.Table, where, tc)
		if err != nil {
			response.ErrorResponse(l, c, http.StatusBadRequest, GeneratorErrorDelete, []string{
				err.Error(),
			})
			return
		}

		output := struct {
			Delete bool `json:"delete"`
		}{
			Delete: true,
		}
		response.Response(l, c, output)

		action.AfterRequest(c)

		// Global after hook
		if generator.GlobalAfterAction != nil {
			generator.GlobalAfterAction(c, module, action)
		}
	}
}
