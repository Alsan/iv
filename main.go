// Command iv is a command-line image viewer using terminal graphics (Sixel,
// iTerm, Kitty).
package main

import (
	"bytes"
	"context"
	"fmt"
	"image/color"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	_ "image/gif"
	"image/jpeg"
	_ "image/png"

	"github.com/xo/resvg"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"

	"github.com/davidbyttow/govips/v2/vips"
	"github.com/kenshaw/colors"
	"github.com/kenshaw/rasterm"
	"github.com/spf13/cobra"
)

var (
	name    = "iv"
	version = "0.0.0-dev"
	verbose bool
)

func main() {
	vips.LoggingSettings(func(domain string, level vips.LogLevel, msg string) {
		fmt.Println(domain, level, msg)
	}, vips.LogLevelError)

	vips.Startup(nil)
	defer vips.Shutdown()

	if err := run(context.Background(), name, version, os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, name, version string, cliargs []string) error {
	bg := colors.FromColor(color.Transparent)
	var (
		bashCompletion       bool
		zshCompletion        bool
		fishCompletion       bool
		powershellCompletion bool
		noDescriptions       bool
	)
	c := &cobra.Command{
		Use:           name + " [flags] <image1> [image2, ..., imageN]",
		Short:         name + ", a command-line image viewer using terminal graphics",
		Version:       version,
		SilenceErrors: true,
		SilenceUsage:  false,
		RunE: func(cmd *cobra.Command, cliargs []string) error {
			// completions and short circuits
			switch {
			case bashCompletion:
				return cmd.GenBashCompletionV2(os.Stdout, !noDescriptions)
			case zshCompletion:
				if noDescriptions {
					return cmd.GenZshCompletionNoDesc(os.Stdout)
				}
				return cmd.GenZshCompletion(os.Stdout)
			case fishCompletion:
				return cmd.GenFishCompletion(os.Stdout, !noDescriptions)
			case powershellCompletion:
				if noDescriptions {
					return cmd.GenPowerShellCompletion(os.Stdout)
				}
				return cmd.GenPowerShellCompletionWithDesc(os.Stdout)
			}
			return do(os.Stdout, bg, cliargs)
		},
	}
	c.SetVersionTemplate("{{ .Name }} {{ .Version }}\n")
	c.InitDefaultHelpCmd()
	c.SetArgs(cliargs[1:])
	flags := c.Flags()
	flags.Var(bg.Pflag(), "bg", "background color")
	// completions
	flags.BoolVar(&bashCompletion, "completion-script-bash", false, "output bash completion script and exit")
	flags.BoolVar(&zshCompletion, "completion-script-zsh", false, "output zsh completion script and exit")
	flags.BoolVar(&fishCompletion, "completion-script-fish", false, "output fish completion script and exit")
	flags.BoolVar(&powershellCompletion, "completion-script-powershell", false, "output powershell completion script and exit")
	flags.BoolVar(&noDescriptions, "no-descriptions", false, "disable descriptions in completion scripts")
	flags.BoolVar(&verbose, "verbose", false, "verbose output")
	// mark hidden
	for _, name := range []string{
		"completion-script-bash", "completion-script-zsh", "completion-script-fish",
		"completion-script-powershell", "no-descriptions",
	} {
		flags.Lookup(name).Hidden = true
	}
	return c.ExecuteContext(ctx)
}

// do renders the specified files to w.
func do(w io.Writer, bg color.Color, args []string) error {
	if !rasterm.Available() {
		return rasterm.ErrTermGraphicsNotAvailable
	}
	resvg.WithBackground(bg)(resvg.Default)
	// collect files
	var files []string
	for i := 0; i < len(args); i++ {
		v, err := open(args[i])
		if err != nil {
			fmt.Fprintf(w, "error: unable to open arg %d: %v\n", i, err)
		}
		files = append(files, v...)
	}
	return render(w, files)
}

func open(name string) ([]string, error) {
	var v []string
	switch fi, err := os.Stat(name); {
	case err == nil && fi.IsDir():
		entries, err := os.ReadDir(name)
		if err != nil {
			return nil, fmt.Errorf("unable to open directory %q: %v", name, err)
		}
		for _, entry := range entries {
			if s := entry.Name(); !entry.IsDir() && extRE.MatchString(s) {
				v = append(v, filepath.Join(name, s))
			}
		}
		sort.Strings(v)
	case err == nil:
		v = append(v, name)
	default:
		return nil, fmt.Errorf("unable to open %q", name)
	}
	return v, nil
}

var extRE = regexp.MustCompile(`(?i)\.(jpe?g|gif|png|svg|bmp|bitmap|tiff?|hei[vc]|avif|webp)$`)

func render(w io.Writer, files []string) error {
	l := len(files)
	if l == 1 {
		if err := renderFile(w, files[0]); err != nil {
			fmt.Fprintf(w, "error: unable to render: %v\n", err)
		}
		return nil
	}

	for i := 0; i < l; i++ {
		fmt.Fprintln(w, files[i]+":")
		if err := renderFile(w, files[i]); err != nil {
			fmt.Fprintf(w, "error: unable to render arg %d: %v\n", i, err)
		}
	}
	return nil
}

// doFile renders the specified file to w.
func renderFile(w io.Writer, file string) error {
	defer duration(track("renderFile"))

	s := time.Now()
	imgref, err := vips.NewImageFromFile(file)
	if err != nil {
		return fmt.Errorf("can't decode %s: %w", file, err)
	}
	defer imgref.Close()
	duration("read from file", s)

	s = time.Now()
	ep := vips.NewJpegExportParams()
	ep.StripMetadata = true
	ep.Quality = 75
	ep.Interlace = true
	ep.OptimizeCoding = true
	ep.SubsampleMode = vips.VipsForeignSubsampleAuto
	ep.TrellisQuant = true
	ep.OvershootDeringing = true
	ep.OptimizeScans = true
	ep.QuantTable = 3

	buf, _, err := imgref.ExportJpeg(ep)
	if err != nil {
		return fmt.Errorf("can't export %s: %w", file, err)
	}
	duration("export to byte buffer", s)

	s = time.Now()
	img, err := jpeg.Decode(bytes.NewBuffer(buf))
	if err != nil {
		return fmt.Errorf("can't decode %s: %w", file, err)
	}
	duration("decode as go image", s)

	s = time.Now()
	if err := rasterm.Encode(w, img); err != nil {
		return fmt.Errorf("can't encode %s: %w", file, err)
	}
	duration("encode as rasterm image", s)

	return nil
}

func track(msg string) (string, time.Time) {
	return msg, time.Now()
}

func duration(msg string, start time.Time) {
	if verbose {
		fmt.Printf("%s: %v\n", msg, time.Since(start))
	}
}
