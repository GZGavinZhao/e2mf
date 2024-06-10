package main

import (
	"archive/tar"
	"bytes"
	"debug/elf"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/getsolus/libeopkg/archive"
	sf "github.com/sa-/slicefunk"
	"github.com/serpent-os/libstone-go/stone1"
	"github.com/xi2/xz"
	"gitlab.com/slxh/go/powerline"
)

type Package struct {
	Name      string
	BuildDeps []stone1.Dependency
	RunDeps   []stone1.Dependency
	Files     []string
	Provides  []stone1.Dependency
}

func DepsToStrings(deps []stone1.Dependency) []string {
	return sf.Map[stone1.Dependency, string](deps, func(d stone1.Dependency) string { return d.String() })
}

func (p Package) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		p.Name: struct {
			RunDeps  []string `json:"build-depends"`
			Files    []string `json:"files"`
			Provides []string `json:"providers"`
		}{RunDeps: DepsToStrings(p.RunDeps), Provides: DepsToStrings(p.Provides), Files: p.Files},
	})
}

type Manifest struct {
	ManifestVersion string    `json:"manifest-version"`
	Packages        []Package `json:"packages"`
	SourceName      string    `json:"source-name"`
	SourceRelease   int       `json:"source-release"`
	SourceVersion   string    `json:"source-version"`
}

func toSonameProvider(dynName string, machine elf.Machine) string {
	switch machine {
	case elf.EM_X86_64:
		return fmt.Sprintf("%s(x86_64)", dynName)
	case elf.EM_386:
		return fmt.Sprintf("%s(x86)", dynName)
	default:
		return fmt.Sprintf("%s(unknown)", dynName)
	}
}

func getProvidersAndDepends(r io.ReaderAt) (p []stone1.Dependency, d []stone1.Dependency, err error) {
	providers := make(map[string]stone1.Dependency)
	depends := make(map[string]stone1.Dependency)

	file, err := elf.NewFile(r)
	if err != nil {
		// if err == io.EOF {
		// 	err = nil
		// }

		return
		// slog.Error("Failed to open reader as ELF", "error", err)
		// os.Exit(1)
	}
	defer file.Close()
	// fmt.Println("Machine:", file.Machine.String())

	switch file.Type {
	case elf.ET_DYN:
		symbols, err := file.DynamicSymbols()
		if err != nil {
			return p, d, fmt.Errorf("Failed to get dynamic symbols: %w", err)
		}

		dynName, err := file.DynString(elf.DT_SONAME)
		if err != nil {
			return p, d, fmt.Errorf("Failed to get DT_SONAME: %w", err)
		}
		if len(dynName) > 0 {
			soName := dynName[0]

			for _, symbol := range symbols {
				stBind := elf.ST_BIND(symbol.Info)
				if (stBind & elf.STB_WEAK) == elf.STB_WEAK {
					continue
				}
				if symbol.Section == elf.SHN_UNDEF {
					continue
				}

				provider := stone1.Dependency{
					Kind: stone1.SharedLibary,
					Name: toSonameProvider(soName, file.Machine),
				}
				providers[provider.String()] = provider
			}
		} else {
			// slog.Error("DT_SONAME is empty")
			// os.Exit(1)
		}
		fallthrough
	case elf.ET_EXEC, elf.ET_REL:
		libs, err := file.ImportedLibraries()
		if err != nil {
			if err == elf.ErrNoSymbols {
				break
			}
			return p, d, fmt.Errorf("Failed to get imported libraries: %w", err)
		}

		for _, lib := range libs {
			if len(lib) == 0 {
				continue
			}

			depend := stone1.Dependency{
				Kind: stone1.SharedLibary,
				Name: toSonameProvider(lib, file.Machine),
			}

			// depend := toSonameProvider(lib, file.Machine)
			depends[depend.String()] = depend
		}

		// symbols, err := file.ImportedSymbols()
		// if err != nil {
		// 	if err == elf.ErrNoSymbols {
		// 		break
		// 	}
		// 	slog.Error("Failed to get symbols", "error", err)
		// 	os.Exit(1)
		// }

		// for _, symbol := range symbols {
		// 	fmt.Println("symbol:", symbol.Name, "library:", symbol.Library)
		// }
	default:
	}

	p = make([]stone1.Dependency, len(providers))
	i := 0
	for _, provider := range providers {
		p[i] = provider
		i++
	}

	d = make([]stone1.Dependency, len(depends))
	i = 0
	for _, depend := range depends {
		d[i] = depend
		i++
	}

	return
}

func main() {
	slog.SetDefault(slog.New(powerline.NewHandler(os.Stderr, &powerline.HandlerOptions{
		Level: slog.LevelDebug,
	})))

	fmt.Println(os.Args)
	input := os.Args[1]

	manifest := Manifest{
		ManifestVersion: "0.2",
		Packages:        []Package{},
	}

	pkg := Package{}

	// if filepath.Ext(input) == ".eopkg" {
	eopkgFile, err := archive.OpenAll(input)
	if err != nil {
		slog.Error("Failed to open eopkg archive", "path", input, "error", err)
		os.Exit(1)
	}
	defer eopkgFile.Close()

	manifest.SourceName = eopkgFile.Meta.Source.Name
	manifest.SourceRelease = eopkgFile.Meta.Package.GetRelease()
	manifest.SourceVersion = eopkgFile.Meta.Package.GetVersion()
	pkg.Name = eopkgFile.Meta.Package.Name

	installTarXZInZip := eopkgFile.FindFile("install.tar.xz")
	if installTarXZInZip == nil {
		slog.Error("Eopkg doesn't contain install.tar.xz!")
		os.Exit(1)
	}

	installTarXZ, err := installTarXZInZip.Open()
	if err != nil {
		slog.Error("Failed to open install.tar.xz in ZIP archive", "error", err)
		os.Exit(1)
	}
	defer installTarXZ.Close()

	installTarXZReader, err := xz.NewReader(installTarXZ, 0)
	if err != nil {
		slog.Error("Failed to open install.tar.xz as an XZ archive", "error", err)
		os.Exit(1)
	}

	installTarReader := tar.NewReader(installTarXZReader)
	if installTarReader == nil {
		slog.Error("Failed to open install.tar.xz as TAR file!")
	}

	for {
		// slog.Debug("Reading next...")
		hdr, err := installTarReader.Next()
		if err == io.EOF {
			break
		} else if err != nil {
			slog.Error("Failed to get next file of install.tar!", "error", err)
			os.Exit(1)
		}

		pkg.Files = append(pkg.Files, filepath.Join("/", hdr.Name))
		// fmt.Println("Scanning", hdr.Name)
		// if filepath.Ext(hdr.Name) == ".a" {
		// 	fmt.Println("Probably static library, skipping...")
		// 	continue
		// }

		var buf bytes.Buffer
		if _, err := io.Copy(&buf, installTarReader); err != nil {

		}
		provides, depends, err := getProvidersAndDepends(bytes.NewReader(buf.Bytes()))
		if err != nil {
			if strings.Contains(err.Error(), "bad magic number") {
				continue
			} else if err == io.EOF {
				// symlink
				continue
			} else {
				slog.Error("Obtain providers/depends failed", "error", err)
				os.Exit(1)
			}
		}

		pkg.RunDeps = depends
		pkg.Provides = provides
		// fmt.Println("File:", hdr.Name, "Providers:", provides, "Depends:", depends)
	}

	manifest.Packages = append(manifest.Packages, pkg)
	if b, err := json.MarshalIndent(manifest, "", "    "); err == nil {
		fmt.Println(string(b))
	} else {
		slog.Error("Failed to marshal to json", "error", err)
		os.Exit(1)
	}
}
