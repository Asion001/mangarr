// Package upscaling implements the download pipeline's processing stage:
// it upscales pages narrower than the profile's minimum width through an
// upscale module and keeps every other page untouched.
package upscaling

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Asion001/mangarr/internal/downloads"
	"github.com/Asion001/mangarr/internal/imagecheck"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/upscale"
	"github.com/Asion001/mangarr/internal/progress"
)

type Processor struct {
	mods *modules.Manager
	// Online (optional) reports whether an upscaler instance is reachable
	// (e.g. a desktop GPU node that may be switched off).
	Online func(def model.ProviderDefinition) bool
	// Fixed (optional) is the only upscaler to use, in place of the
	// modules: a worker processing pages upscales with its own engine.
	Fixed upscale.Module
}

func New(m *modules.Manager) *Processor { return &Processor{mods: m} }

// NewFixed is a processor that always upscales with up.
func NewFixed(up upscale.Module) *Processor { return &Processor{Fixed: up} }

// ErrNoUpscaler means no upscaler is configured or none is reachable now.
type ErrNoUpscaler struct{ Reason string }

func (e ErrNoUpscaler) Error() string { return "no upscaler available: " + e.Reason }

// upscaler returns the configured upscaler, or the first reachable one by priority.
func (p *Processor) upscaler(ctx context.Context, cfg model.UpscaleConfig) (upscale.Module, *upscale.Info, error) {
	if p.Fixed != nil {
		info, err := p.Fixed.Info(ctx)
		if err == nil && len(info.Models) == 0 {
			err = ErrNoUpscaler{"this machine's upscaler has no models"}
		}
		return p.Fixed, info, err
	}
	if cfg.UpscalerID > 0 {
		m, _, err := modules.GetAs[upscale.Module](p.mods, cfg.UpscalerID)
		if err != nil {
			return nil, nil, err
		}
		info, err := m.Info(ctx)
		return m, info, err
	}
	list := modules.ActiveAs[upscale.Module](p.mods, modules.KindUpscale)
	if len(list) == 0 {
		return nil, nil, ErrNoUpscaler{"no upscaler module is configured"}
	}
	var errs []string
	for _, t := range ranked(ctx, list) {
		if p.Online != nil && !p.Online(t.Def) {
			errs = append(errs, t.Def.Name+": offline")
			continue
		}
		ictx, cancel := context.WithTimeout(ctx, 20*time.Second)
		info, err := t.Instance.Info(ictx)
		cancel()
		if err == nil && len(info.Models) > 0 {
			return t.Instance, info, nil
		}
		if err == nil {
			err = errors.New("no models")
		}
		errs = append(errs, t.Def.Name+": "+err.Error())
	}
	return nil, nil, ErrNoUpscaler{strings.Join(errs, "; ")}
}

// ranked orders the upscalers by where they stand now: the workers module
// takes the priority of its best online worker, so this server and each
// worker sit in one list. One that has nobody to run the work keeps its own
// priority and fails its Info as before.
func ranked(ctx context.Context, list []modules.Typed[upscale.Module]) []modules.Typed[upscale.Module] {
	rank := make(map[int64]int, len(list))
	for _, t := range list {
		rank[t.Def.ID] = t.Def.Priority
		if r, ok := t.Instance.(upscale.Ranked); ok {
			if n, ok := r.Rank(ctx); ok {
				rank[t.Def.ID] = n
			}
		}
	}
	out := append([]modules.Typed[upscale.Module](nil), list...)
	sort.SliceStable(out, func(i, j int) bool { return rank[out[i].Def.ID] < rank[out[j].Def.ID] })
	return out
}

// ChooseScale picks the smallest supported scale that brings width to at
// least minWidth (or the largest scale when none does).
func ChooseScale(width, minWidth int, scales []int) int {
	s := append([]int(nil), scales...)
	sort.Ints(s)
	for _, x := range s {
		if x >= 2 && width*x >= minWidth {
			return x
		}
	}
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] >= 2 {
			return s[i]
		}
	}
	return 0
}

// SourceFormat keeps each upscaled page in the format it was downloaded in.
const SourceFormat = "source"

// OutputFormat is the format an upscaled page is written in: the profile's
// format, or with SourceFormat the page's own (PNG for anything the
// upscaler can't write).
func OutputFormat(profile, page string) string {
	switch profile {
	case "":
		return "webp"
	case SourceFormat:
		if page == "jpeg" || page == "webp" {
			return page
		}
		return "png"
	}
	return profile
}

// NeedsUpscale reports whether a page should be upscaled.
// A landscape two-page spread is judged by the width of each half.
func NeedsUpscale(pg downloads.PageFile, minWidth int) bool {
	if minWidth <= 0 || pg.Width <= 0 || model.PageWidth(pg.Width, pg.Height) >= minWidth {
		return false
	}
	switch pg.Format {
	case "jpeg", "png", "webp", "bmp":
		return true
	}
	return false // gif (animations), avif, jxl are left alone
}

func (p *Processor) Process(ctx context.Context, cfg model.UpscaleConfig, pages []downloads.PageFile, workDir string) ([]downloads.PageFile, bool, string, error) {
	var todo []int
	for i, pg := range pages {
		if NeedsUpscale(pg, cfg.MinWidth) {
			todo = append(todo, i)
		}
	}
	if len(todo) == 0 {
		return pages, false, "", nil
	}
	up, info, err := p.upscaler(ctx, cfg)
	if err != nil {
		return nil, false, "", err
	}
	var mdl *upscale.Model
	for i := range info.Models {
		if info.Models[i].Name == cfg.Model {
			mdl = &info.Models[i]
		}
	}
	if mdl == nil {
		if len(info.Models) == 0 {
			return nil, false, "", errors.New("upscaler has no models")
		}
		mdl = &info.Models[0]
	}
	// group pages by the scale and output format they need so each batch is
	// one engine run
	type batch struct {
		scale    int
		format   string
		maxWidth int
	}
	groups := map[batch][]int{}
	for _, i := range todo {
		s := ChooseScale(model.PageWidth(pages[i].Width, pages[i].Height), cfg.MinWidth, mdl.Scales)
		if s == 0 {
			continue
		}
		b := batch{s, OutputFormat(cfg.Format, pages[i].Format), cfg.MaxWidth}
		if b.maxWidth > 0 && pages[i].Width > pages[i].Height {
			b.maxWidth *= 2 // a spread holds two pages
		}
		groups[b] = append(groups[b], i)
	}
	out := append([]downloads.PageFile(nil), pages...)
	outDir := filepath.Join(workDir, "upscaled")
	if err := os.MkdirAll(outDir, 0o775); err != nil {
		return nil, false, "", err
	}
	done, total := 0, 0
	for _, idxs := range groups {
		total += len(idxs)
	}
	progress.Report(ctx, progress.Event{Stage: progress.StageUpscale, Total: total})
	// a worker may run its own model instead of the profile's: the file
	// records what the pages were really upscaled with
	ctx, used := upscale.WithUsed(ctx)
	for b, group := range groups {
		for _, idxs := range chunks(pages, group, b.scale) {
			if err := p.upscaleChunk(ctx, up, mdl, cfg, b.format, b.scale, b.maxWidth, pages, idxs, out, outDir); err != nil {
				return nil, false, "", err
			}
			done += len(idxs)
			progress.Report(ctx, progress.Event{Stage: progress.StageUpscale, Done: done, Total: total})
		}
	}
	name := mdl.Name
	if names := used(); len(names) > 0 {
		name = strings.Join(names, ", ")
	}
	return out, true, name, nil
}

// ChunkPages is how many pages go to the upscaler at once: short runs keep
// memory bounded, stay far from the upscaler's time limit on slow GPUs and
// show progress.
var ChunkPages = 8

// ChunkPixels caps the upscaled pixels in one run as well. Every upscaled
// page of a run is held in memory until the run is done, and eight 4x
// webtoon strips come to well over a gigabyte — more than a worker with a
// memory limit has next to the upscaler itself. A page bigger than this
// still goes, on its own.
var ChunkPixels = 120_000_000

// chunks splits a group of pages into runs of at most ChunkPages pages and
// ChunkPixels upscaled pixels.
func chunks(pages []downloads.PageFile, group []int, scale int) [][]int {
	var out [][]int
	var cur []int
	px := 0
	for _, i := range group {
		n := pages[i].Width * pages[i].Height * scale * scale
		if len(cur) > 0 && (len(cur) >= ChunkPages || px+n > ChunkPixels) {
			out = append(out, cur)
			cur, px = nil, 0
		}
		cur = append(cur, i)
		px += n
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

func (p *Processor) upscaleChunk(ctx context.Context, up upscale.Module, mdl *upscale.Model, cfg model.UpscaleConfig, format string, scale, maxWidth int,
	pages []downloads.PageFile, idxs []int, out []downloads.PageFile, outDir string) error {
	imgs := make([]upscale.Image, 0, len(idxs))
	for _, i := range idxs {
		data, err := os.ReadFile(pages[i].Path)
		if err != nil {
			return err
		}
		imgs = append(imgs, upscale.Image{Name: pages[i].Name, Data: data})
	}
	res, err := up.Upscale(ctx, imgs, upscale.Params{Model: mdl.Name, Scale: scale, Noise: cfg.Noise, Format: format,
		Quality: cfg.Quality, MaxWidth: maxWidth})
	if err != nil {
		return err
	}
	if len(res) != len(idxs) {
		return fmt.Errorf("upscaler returned %d of %d pages", len(res), len(idxs))
	}
	for k, i := range idxs {
		info, err := imagecheck.Detect(res[k].Data)
		if err != nil {
			return fmt.Errorf("upscaled %s: %w", pages[i].Name, err)
		}
		base := strings.TrimSuffix(pages[i].Name, filepath.Ext(pages[i].Name))
		name := base + imagecheck.Ext(info.Format)
		path := filepath.Join(outDir, name)
		if err := os.WriteFile(path, res[k].Data, 0o664); err != nil {
			return err
		}
		out[i] = downloads.PageFile{Name: name, Path: path, Format: info.Format, Width: info.Width, Height: info.Height}
	}
	return nil
}
