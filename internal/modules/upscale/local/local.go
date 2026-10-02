// Package local is the built-in upscaler: it runs the ncnn/Vulkan tools of
// the full image inside the mangarr server (MANGARR_MODE=integrated), so a
// single container can upscale with the host's GPU.
package local

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/upscale"
	"github.com/Asion001/mangarr/internal/upscaler"
	"github.com/Asion001/mangarr/internal/version"
)

type Settings struct {
	ToolsDir string `json:"toolsDir" label:"Tools folder" order:"1" help:"Where the waifu2x/Real-CUGAN/Real-ESRGAN ncnn binaries are (included in the full image)."`
	GPU      string `json:"gpu" label:"GPU" order:"2" placeholder:"auto" help:"auto, or comma-separated Vulkan device indices such as 0,1 (pass /dev/dri to the container for Intel/AMD)."`
	Threads  string `json:"threads" label:"Threads" advanced:"true" order:"3" placeholder:"1:2:2" help:"load:proc:save threads (ncnn -j)."`
	Tile     int    `json:"tile" label:"Tile size" advanced:"true" order:"4" help:"0 = automatic; lower it when the GPU runs out of memory."`
	// Model is what this server upscales with; empty uses the profile's.
	Model string `json:"model" label:"Upscale model" order:"5" help:"Empty uses the profile's model."`
}

func init() {
	modules.Register(&modules.Implementation{
		Kind: modules.KindUpscale, Name: "local", DisplayName: "Built-in (this server)",
		Description: "Upscales inside mangarr with the ncnn tools of the full image (no separate worker).",
		Settings:    func() any { return &Settings{ToolsDir: "/opt/upscalers", GPU: "auto"} },
		New: func(deps modules.Deps, s any) (modules.Instance, error) {
			st := s.(*Settings)
			if _, err := upscaler.ParseGPUs(st.GPU); err != nil {
				return nil, fmt.Errorf("GPU: %w", err)
			}
			tmp := filepath.Join(deps.DataDir, "tmp")
			if deps.DataDir == "" {
				tmp = os.TempDir()
			}
			_ = os.MkdirAll(tmp, 0o775)
			runner := upscaler.CLIRunner{ToolsDir: st.ToolsDir, GPU: st.GPU, Threads: st.Threads, Tile: st.Tile, Log: deps.Log}
			log := deps.Log
			srv := upscaler.NewServer(upscaler.Config{TmpDir: tmp, GPU: st.GPU, Version: version.Version}, runner, log)
			return &Module{srv: srv, dir: st.ToolsDir, model: st.Model}, nil
		},
	})
}

type Module struct {
	srv *upscaler.Server
	dir string
	// model replaces the profile's model when this server has it.
	model string
}

func (m *Module) Test(ctx context.Context) error {
	if len(m.srv.Info().Models) == 0 {
		return fmt.Errorf("no upscaler tools found in %s (use the full image on amd64)", m.dir)
	}
	return nil
}

func (m *Module) Info(ctx context.Context) (*upscale.Info, error) {
	in := m.srv.Info()
	out := &upscale.Info{Version: in.Version, Devices: in.Devices}
	for _, e := range in.Models {
		out.Models = append(out.Models, upscale.Model{Name: e.Name, Description: e.Description, Scales: e.Scales, NoiseLevels: e.NoiseLevels})
	}
	return out, nil
}

func (m *Module) Upscale(ctx context.Context, images []upscale.Image, p upscale.Params) ([]upscale.Image, error) {
	in := make([]upscaler.Image, len(images))
	for i, img := range images {
		in[i] = upscaler.Image{Name: img.Name, Data: img.Data}
	}
	if m.model != "" && m.model != p.Model && !p.Pinned {
		for _, e := range m.srv.Info().Models {
			if e.Name == m.model {
				p.Model, p.Scale = e.Name, upscale.FitScale(p.Scale, e.Scales)
				break
			}
		}
	}
	out, _, err := m.srv.ProcessDevice(ctx, upscaler.Params{Model: p.Model, Scale: p.Scale, Noise: p.Noise, Format: p.Format, Quality: p.Quality, MaxWidth: p.MaxWidth}, in)
	if err != nil {
		return nil, err
	}
	upscale.ReportUsed(ctx, p.Model)
	res := make([]upscale.Image, len(out))
	for i, img := range out {
		res[i] = upscale.Image{Name: img.Name, Data: img.Data}
	}
	return res, nil
}

var _ upscale.Module = (*Module)(nil)
