// Package envcfg pins configuration with environment variables, so a
// container can be "set in stone":
//
//   - settings fields: MANGARR_<DOC>_<FIELD>, e.g. MANGARR_DOWNLOADS_MAX_CONCURRENT=2
//   - the API key: MANGARR_API_KEY
//   - root folders: MANGARR_ROOT_FOLDERS=/data/manga/en,/data/manga/ja
//   - module instances: MANGARR_MODULE_<NAME>_IMPL=source/suwayomi plus
//     MANGARR_MODULE_<NAME>_<FIELD>, _NAME, _ENABLED, _ORDER, _SERIES_TAGS, _EVENTS
//
// Pinned values are shown as locked in the UI. Settings overlays live in
// memory only (removing a variable restores the saved value); modules and
// root folders are upserted into the database so their IDs stay stable.
package envcfg

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/Asion001/mangarr/internal/config"
	"github.com/Asion001/mangarr/internal/settings"
)

const Prefix = "MANGARR_"

// Var documents one supported variable.
type Var struct {
	Name        string `json:"name"`
	Scope       string `json:"scope" enum:"core,settings,rootfolders,module"`
	Target      string `json:"target"`
	Type        string `json:"type"`
	Default     string `json:"default"`
	Description string `json:"description"`
	Secret      bool   `json:"secret"`
	Set         bool   `json:"set"`
	// Value is the current value (masked for secrets).
	Value string `json:"value,omitempty"`
}

// Snake converts a JSON field name to the variable suffix: maxConcurrent → MAX_CONCURRENT.
func Snake(name string) string {
	var b strings.Builder
	rs := []rune(name)
	for i, r := range rs {
		if i > 0 && unicode.IsUpper(r) && (unicode.IsLower(rs[i-1]) || unicode.IsDigit(rs[i-1]) ||
			(i+1 < len(rs) && unicode.IsLower(rs[i+1]))) {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToUpper(r))
	}
	return b.String()
}

// field is a settable leaf of a settings struct.
type field struct {
	path   string // JSON path "a.b"
	env    string
	typ    reflect.Type
	desc   string
	secret bool
	def    any
}

// fieldsOf lists the leaves of struct v (a pointer) with their variable names.
func fieldsOf(v any, envPrefix string) []field {
	var out []field
	var walk func(rv reflect.Value, path, env string)
	walk = func(rv reflect.Value, path, env string) {
		rt := rv.Type()
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			if !f.IsExported() {
				continue
			}
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "" || name == "-" {
				continue
			}
			tag := f.Tag.Get("env")
			if tag == "-" {
				continue
			}
			p := name
			if path != "" {
				p = path + "." + name
			}
			e := env + "_" + Snake(name)
			if strings.HasPrefix(tag, "=") {
				e = Prefix + tag[1:]
			}
			fv := rv.Field(i)
			if f.Type.Kind() == reflect.Struct && f.Type.PkgPath() != "time" {
				walk(fv, p, e)
				continue
			}
			out = append(out, field{path: p, env: e, typ: f.Type, desc: f.Tag.Get("desc"),
				secret: f.Tag.Get("secret") == "true", def: fv.Interface()})
		}
	}
	walk(reflect.ValueOf(v).Elem(), "", Prefix+envPrefix)
	return out
}

// Parse converts a variable value to a value of type t (as a JSON-able Go value).
// Lists are comma-separated, maps are k=v pairs; JSON is accepted for both.
func Parse(t reflect.Type, s string) (any, error) {
	s = strings.TrimSpace(s)
	switch t.Kind() {
	case reflect.String:
		return s, nil
	case reflect.Bool:
		return strconv.ParseBool(s)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.ParseInt(s, 10, 64)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.ParseUint(s, 10, 64)
	case reflect.Float32, reflect.Float64:
		return strconv.ParseFloat(s, 64)
	case reflect.Slice:
		if strings.HasPrefix(s, "[") {
			return parseJSON(t, s)
		}
		out := []any{}
		for _, part := range strings.Split(s, ",") {
			if part = strings.TrimSpace(part); part == "" {
				continue
			}
			v, err := Parse(t.Elem(), part)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case reflect.Map:
		if strings.HasPrefix(s, "{") || t.Key().Kind() != reflect.String {
			return parseJSON(t, s)
		}
		out := map[string]any{}
		for _, part := range strings.Split(s, ",") {
			if part = strings.TrimSpace(part); part == "" {
				continue
			}
			k, val, ok := strings.Cut(part, "=")
			if !ok {
				return nil, fmt.Errorf("expected key=value pairs, got %q", part)
			}
			v, err := Parse(t.Elem(), val)
			if err != nil {
				return nil, err
			}
			out[strings.TrimSpace(k)] = v
		}
		return out, nil
	default:
		return parseJSON(t, s)
	}
}

func parseJSON(t reflect.Type, s string) (any, error) {
	ptr := reflect.New(t)
	if err := json.Unmarshal([]byte(s), ptr.Interface()); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return ptr.Elem().Interface(), nil
}

func typeName(t reflect.Type) string {
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "bool"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "int"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Slice:
		if k := t.Elem().Kind(); k == reflect.String || k == reflect.Int64 || k == reflect.Int {
			return "list"
		}
		return "json"
	case reflect.Map:
		return "key=value list"
	default:
		return "json"
	}
}

func formatDefault(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []string:
		return strings.Join(x, ",")
	case nil:
		return ""
	}
	rv := reflect.ValueOf(v)
	if (rv.Kind() == reflect.Slice || rv.Kind() == reflect.Map) && rv.Len() == 0 {
		return ""
	}
	b, _ := json.Marshal(v)
	return strings.Trim(string(b), `"`)
}

func mask(v string, secret bool) string {
	if secret && v != "" {
		return "********"
	}
	return v
}

// ApplySettings pins settings fields from env into the store.
func ApplySettings(env map[string]string, st *settings.Store) error {
	var errs []error
	for _, d := range settings.Docs {
		overlay := map[string]any{}
		var locks []settings.Lock
		for _, f := range fieldsOf(d.Default(), d.EnvPrefix) {
			raw, ok := env[f.env]
			if !ok {
				continue
			}
			v, err := Parse(f.typ, raw)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", f.env, err))
				continue
			}
			setPath(overlay, strings.Split(f.path, "."), v)
			locks = append(locks, settings.Lock{Path: f.path, Env: f.env})
		}
		b, _ := json.Marshal(overlay)
		st.SetOverlay(d.Key, b, locks)
	}
	return errors.Join(errs...)
}

func setPath(m map[string]any, parts []string, v any) {
	for _, p := range parts[:len(parts)-1] {
		next, ok := m[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[p] = next
		}
		m = next
	}
	m[parts[len(parts)-1]] = v
}

// SettingsVars documents every settings variable.
func SettingsVars(env map[string]string) []Var {
	var out []Var
	for _, d := range settings.Docs {
		for _, f := range fieldsOf(d.Default(), d.EnvPrefix) {
			v, set := env[f.env]
			out = append(out, Var{Name: f.env, Scope: "settings", Target: d.Name + "." + f.path, Type: typeName(f.typ),
				Default: mask(formatDefault(f.def), f.secret), Description: f.desc, Secret: f.secret, Set: set, Value: mask(v, f.secret)})
		}
	}
	return out
}

// CoreVars documents the process-level variables.
func CoreVars(env map[string]string) []Var {
	var out []Var
	for _, d := range config.Vars {
		v, set := env[d.Name]
		out = append(out, Var{Name: d.Name, Scope: "core", Type: "string", Default: d.Default, Description: d.Description, Set: set, Value: v})
	}
	v, set := env[RootFoldersVar]
	out = append(out, Var{Name: RootFoldersVar, Scope: "rootfolders", Type: "list", Description: "Root folders to create and lock, comma-separated; append |lang to set a language (/data/manga/ja|ja), or |* for the automatic folder that gets a subfolder per language (/data/manga|*).", Set: set, Value: v})
	return out
}

// All documents every variable, including module variables that are set.
func All(env map[string]string) []Var {
	out := CoreVars(env)
	out = append(out, SettingsVars(env)...)
	out = append(out, ModuleVars(env)...)
	return out
}

// Unknown lists MANGARR_* variables that nothing reads (likely typos).
func Unknown(env map[string]string) []string {
	known := map[string]bool{}
	for _, v := range All(env) {
		known[v.Name] = true
	}
	var out []string
	for k := range env {
		if !known[k] && !strings.HasPrefix(k, ModulePrefix) && !strings.HasPrefix(k, "MANGARR_TEST_") && !extraKnown[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// extraKnown lists variables read elsewhere (e.g. by other process modes).
var extraKnown = map[string]bool{
	"MANGARR_UPSCALER_LISTEN": true, "MANGARR_UPSCALER_TOOLS_DIR": true, "MANGARR_UPSCALER_GPU": true, "MANGARR_UPSCALER_THREADS": true,
	"MANGARR_UPSCALER_TILE": true, "MANGARR_UPSCALER_TOKEN": true, "MANGARR_UPSCALER_TIMEOUT": true, "MANGARR_UPSCALER_TMP_DIR": true,
	"MANGARR_UPSCALER_CWEBP": true, "MANGARR_SERVER_URL": true, "MANGARR_NODE_NAME": true, "MANGARR_NODE_URL": true,
}
