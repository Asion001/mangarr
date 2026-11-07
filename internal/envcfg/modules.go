package envcfg

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
)

const (
	ModulePrefix   = Prefix + "MODULE_"
	RootFoldersVar = Prefix + "ROOT_FOLDERS"
)

// metaSuffixes are module variables that aren't settings fields.
var metaSuffixes = []string{"IMPL", "NAME", "ENABLED", "ORDER", "SERIES_TAGS", "EVENTS"}

type moduleField struct {
	name   string // JSON name
	typ    reflect.Type
	secret bool
}

func moduleFields(impl *modules.Implementation) map[string]moduleField {
	out := map[string]moduleField{}
	rv := reflect.ValueOf(impl.Settings())
	if rv.Kind() == reflect.Pointer {
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return out
	}
	rt := rv.Type()
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if !f.IsExported() || name == "" || name == "-" {
			continue
		}
		out[Snake(name)] = moduleField{name: name, typ: f.Type, secret: f.Tag.Get("secret") == "true"}
	}
	return out
}

// envModule is a module instance declared by variables.
type envModule struct {
	key       string // <NAME>
	impl      *modules.Implementation
	lock      *modules.EnvLock
	tagLabels []string
	vars      []Var
	// name is the display name: _NAME, or derived from <NAME> (not pinned then).
	name string
}

func (e *envModule) managedBy() string { return "env:" + e.key }

func lookupImpl(v string) (*modules.Implementation, error) {
	v = strings.ToLower(strings.TrimSpace(v))
	if kind, name, ok := strings.Cut(v, "/"); ok {
		if impl, found := modules.Lookup(modules.Kind(kind), name); found {
			return impl, nil
		}
		return nil, fmt.Errorf("unknown module implementation %q", v)
	}
	var found []*modules.Implementation
	for _, k := range []modules.Kind{modules.KindSource, modules.KindMetadata, modules.KindLibrary, modules.KindNotify, modules.KindUpscale} {
		if impl, ok := modules.Lookup(k, v); ok {
			found = append(found, impl)
		}
	}
	switch len(found) {
	case 0:
		return nil, fmt.Errorf("unknown module implementation %q (use <kind>/<implementation>)", v)
	case 1:
		return found[0], nil
	default:
		return nil, fmt.Errorf("module implementation %q is ambiguous, use <kind>/%s", v, v)
	}
}

func titleCase(key string) string {
	words := strings.Split(strings.ToLower(key), "_")
	for i, w := range words {
		if w != "" {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

// parseModules reads MANGARR_MODULE_* variables.
func parseModules(env map[string]string) ([]*envModule, error) {
	byKey := map[string]*envModule{}
	var keys []string
	for k, v := range env {
		if !strings.HasPrefix(k, ModulePrefix) || !strings.HasSuffix(k, "_IMPL") {
			continue
		}
		key := strings.TrimSuffix(strings.TrimPrefix(k, ModulePrefix), "_IMPL")
		if key == "" {
			return nil, fmt.Errorf("%s: missing module name", k)
		}
		impl, err := lookupImpl(v)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		m := &envModule{key: key, impl: impl, lock: &modules.EnvLock{Fields: map[string]string{}, Values: map[string]any{}, Meta: map[string]string{}}}
		m.vars = append(m.vars, Var{Name: k, Scope: "module", Target: strings.ToLower(key) + ".implementation", Type: "string", Set: true, Value: v,
			Description: "Implementation of module " + key + " (" + string(impl.Kind) + "/" + impl.Name + ")."})
		byKey[key] = m
		keys = append(keys, key)
	}
	// longest names first so MODULE_KOMGA_2_URL matches KOMGA_2 before KOMGA
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })

	var errs []string
	for k, raw := range env {
		if !strings.HasPrefix(k, ModulePrefix) || strings.HasSuffix(k, "_IMPL") && byKey[strings.TrimSuffix(strings.TrimPrefix(k, ModulePrefix), "_IMPL")] != nil {
			continue
		}
		var m *envModule
		var suffix string
		for _, key := range keys {
			if strings.HasPrefix(k, ModulePrefix+key+"_") {
				m, suffix = byKey[key], strings.TrimPrefix(k, ModulePrefix+key+"_")
				break
			}
		}
		if m == nil {
			errs = append(errs, fmt.Sprintf("%s: no matching %s<NAME>_IMPL variable", k, ModulePrefix))
			continue
		}
		if err := m.set(k, suffix, raw); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", k, err))
		}
	}
	if len(errs) > 0 {
		sort.Strings(errs)
		return nil, fmt.Errorf("module variables: %s", strings.Join(errs, "; "))
	}
	out := make([]*envModule, 0, len(byKey))
	for _, m := range byKey {
		if m.lock.Name != nil {
			m.name = *m.lock.Name
		} else if strings.EqualFold(m.key, m.impl.Name) {
			m.name = m.impl.DisplayName
		} else {
			m.name = titleCase(m.key)
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key < out[j].key })
	return out, nil
}

func (m *envModule) set(envName, suffix, raw string) error {
	target := strings.ToLower(m.key) + "."
	doc := Var{Name: envName, Scope: "module", Type: "string", Set: true, Value: raw}
	switch suffix {
	case "NAME":
		v := strings.TrimSpace(raw)
		m.lock.Name, m.lock.Meta["name"] = &v, envName
		doc.Target = target + "name"
	case "ENABLED":
		b, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return err
		}
		m.lock.Enabled, m.lock.Meta["enabled"] = &b, envName
		doc.Target, doc.Type = target+"enabled", "bool"
	case "ORDER":
		n, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			return err
		}
		m.lock.Priority, m.lock.Meta["priority"] = &n, envName
		doc.Target, doc.Type = target+"priority", "int"
	case "SERIES_TAGS":
		m.tagLabels, m.lock.Meta["tags"] = splitList(raw), envName
		doc.Target, doc.Type = target+"tags", "list"
	case "EVENTS":
		m.lock.Events, m.lock.Meta["events"] = splitList(raw), envName
		doc.Target, doc.Type = target+"events", "list"
	default:
		fields := moduleFields(m.impl)
		f, ok := fields[suffix]
		if !ok {
			var valid []string
			for s := range fields {
				valid = append(valid, s)
			}
			valid = append(valid, metaSuffixes[1:]...)
			sort.Strings(valid)
			return fmt.Errorf("%s has no setting %q (valid: %s)", m.impl.Name, suffix, strings.Join(valid, ", "))
		}
		v, err := Parse(f.typ, raw)
		if err != nil {
			return err
		}
		m.lock.Values[f.name], m.lock.Fields[f.name] = v, envName
		doc.Target, doc.Type, doc.Secret, doc.Value = target+f.name, typeName(f.typ), f.secret, mask(raw, f.secret)
	}
	m.vars = append(m.vars, doc)
	return nil
}

func splitList(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ModuleVars documents the module variables that are set.
func ModuleVars(env map[string]string) []Var {
	ms, err := parseModules(env)
	if err != nil {
		return nil
	}
	var out []Var
	for _, m := range ms {
		out = append(out, m.vars...)
	}
	return out
}

// SyncModules upserts module instances declared by variables and registers
// their locks with the manager. Call it before the first Manager.Reload.
func SyncModules(ctx context.Context, d *db.DB, mods *modules.Manager, env map[string]string) error {
	ms, err := parseModules(env)
	if err != nil {
		return err
	}
	locks := map[string]*modules.EnvLock{}
	keep := []string{}
	for _, m := range ms {
		if m.tagLabels != nil {
			ids, err := tagIDs(ctx, d, m.tagLabels)
			if err != nil {
				return err
			}
			m.lock.Tags = ids
		}
		var def model.ProviderDefinition
		err := d.NewSelect().Model(&def).Where("managed_by = ?", m.managedBy()).Limit(1).Scan(ctx)
		if err != nil {
			// adopt an instance created in the UI with the same name
			err = d.NewSelect().Model(&def).
				Where("kind = ? AND implementation = ? AND LOWER(name) = ? AND managed_by = ''", m.impl.Kind, m.impl.Name, strings.ToLower(m.name)).
				Limit(1).Scan(ctx)
		}
		isNew := err != nil
		if isNew {
			def = model.ProviderDefinition{Kind: string(m.impl.Kind), Implementation: m.impl.Name, Name: m.name, Enabled: true, Priority: 25,
				Settings: map[string]any{}, CreatedAt: time.Now().UTC()}
		}
		m.lock.Apply(&def)
		def.ManagedBy = m.managedBy()
		if err := modules.Validate(&def); err != nil {
			return fmt.Errorf("%s%s_*: %w", ModulePrefix, m.key, err)
		}
		def.UpdatedAt = time.Now().UTC()
		if isNew {
			_, err = d.NewInsert().Model(&def).Exec(ctx)
		} else {
			_, err = d.NewUpdate().Model(&def).WherePK().Exec(ctx)
		}
		if err != nil {
			return fmt.Errorf("save module %s: %w", m.key, err)
		}
		locks[def.ManagedBy] = m.lock
		keep = append(keep, def.ManagedBy)
	}
	// instances whose variables were removed become normal instances
	q := d.NewUpdate().Model((*model.ProviderDefinition)(nil)).Set("managed_by = ''").Where("managed_by LIKE 'env:%'")
	if len(keep) > 0 {
		q = q.Where("managed_by NOT IN (?)", bun.List(keep))
	}
	if _, err := q.Exec(ctx); err != nil {
		return err
	}
	mods.SetEnvLocks(locks)
	return nil
}

func tagIDs(ctx context.Context, d *db.DB, labels []string) ([]int64, error) {
	ids := []int64{}
	for _, l := range labels {
		var t model.Tag
		if err := d.NewSelect().Model(&t).Where("LOWER(label) = ?", strings.ToLower(l)).Limit(1).Scan(ctx); err != nil {
			t = model.Tag{Label: l}
			if _, err := d.NewInsert().Model(&t).Exec(ctx); err != nil {
				return nil, err
			}
		}
		ids = append(ids, t.ID)
	}
	return ids, nil
}
