package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type MockeryConfig struct {
	WithExpecter    *bool                    `yaml:"with-expecter,omitempty"`
	Dir             string                   `yaml:"dir,omitempty"`
	Filename        string                   `yaml:"filename,omitempty"`
	StructName      string                   `yaml:"structname,omitempty"`
	Inpackage       *bool                    `yaml:"inpackage,omitempty"`
	Testonly        *bool                    `yaml:"testonly,omitempty"`
	InpackageSuffix *bool                    `yaml:"inpackage-suffix,omitempty"`
	Packages        map[string]PackageConfig `yaml:"packages,omitempty"`
}

type PackageConfig struct {
	Config     *InterfaceConfig           `yaml:"config,omitempty"`
	Interfaces map[string]InterfaceConfig `yaml:"interfaces,omitempty"`
}

type InterfaceConfig struct {
	Dir             string `yaml:"dir,omitempty"`
	Filename        string `yaml:"filename,omitempty"`
	StructName      string `yaml:"structname,omitempty"`
	WithExpecter    *bool  `yaml:"with-expecter,omitempty"`
	Inpackage       *bool  `yaml:"inpackage,omitempty"`
	Testonly        *bool  `yaml:"testonly,omitempty"`
	InpackageSuffix *bool  `yaml:"inpackage-suffix,omitempty"`
	// Можно добавить позже: UnrollVariadic *bool, Case string, Outpkg string, etc.
}

func main() {
	configPath := flag.String("config", ".mockery.yaml", "Путь к конфигу mockery")
	rootDir := flag.String("root", ".", "Корневая директория для сканирования")
	outputPath := flag.String("output", "", "Куда сохранить результат (по умолчанию перезаписывает config)")
	flag.Parse()

	if *rootDir == "" {
		fmt.Println("Укажите -root")
		os.Exit(1)
	}

	var config MockeryConfig
	if data, err := os.ReadFile(*configPath); err == nil {
		_ = yaml.Unmarshal(data, &config) // игнорируем ошибку — может быть пустой файл
	}

	if config.Packages == nil {
		config.Packages = make(map[string]PackageConfig)
	}

	// Глобальные дефолты (можно переопределить в конфиге)
	if config.Dir == "" {
		config.Dir = "{{.InterfaceDir}}"
	}
	if config.Filename == "" {
		config.Filename = "mock_{{.InterfaceName}}.go"
	}
	if config.StructName == "" {
		config.StructName = "Mock{{.InterfaceName}}"
	}
	if config.WithExpecter == nil {
		t := true
		config.WithExpecter = &t
	}

	err := filepath.Walk(*rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".go") {
			return nil
		}

		rel, err := filepath.Rel(*rootDir, filepath.Dir(path))
		if err != nil {
			return err
		}
		pkgPath := filepath.ToSlash(rel)
		if pkgPath == "." {
			pkgPath = ""
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if !strings.HasPrefix(line, "//go:generate mockery") {
				continue
			}

			args := parseMockeryArgs(line)

			name, ok := args["name"]
			if !ok || name == "" {
				continue
			}

			if config.Packages[pkgPath].Interfaces == nil {
				config.Packages[pkgPath] = PackageConfig{Interfaces: make(map[string]InterfaceConfig)}
			}

			iface := config.Packages[pkgPath].Interfaces[name]
			applyArg(&iface, args, "dir", func(v string) { iface.Dir = v })
			applyArg(&iface, args, "filename", func(v string) { iface.Filename = v })
			applyArg(&iface, args, "structname", func(v string) { iface.StructName = v })

			applyBoolArg(&iface, args, "with-expecter", func(v bool) { iface.WithExpecter = &v })
			applyBoolArg(&iface, args, "inpackage", func(v bool) { iface.Inpackage = &v })
			applyBoolArg(&iface, args, "testonly", func(v bool) { iface.Testonly = &v })
			applyBoolArg(&iface, args, "inpackage-suffix", func(v bool) { iface.InpackageSuffix = &v })

			config.Packages[pkgPath].Interfaces[name] = iface
		}
		return scanner.Err()
	})
	if err != nil {
		fmt.Printf("Ошибка сканирования: %v\n", err)
		os.Exit(1)
	}

	outPath := *outputPath
	if outPath == "" {
		outPath = *configPath
	}

	data, err := yaml.Marshal(&config)
	if err != nil {
		fmt.Printf("Ошибка yaml.Marshal: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(outPath, data, 0644); err != nil {
		fmt.Printf("Ошибка записи: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Конфиг обновлён → %s\n", outPath)
	fmt.Printf("Пакетов с интерфейсами: %d\n", len(config.Packages))
}

func parseMockeryArgs(line string) map[string]string {
	args := make(map[string]string)

	// Убираем префикс
	line = strings.TrimPrefix(line, "//go:generate")
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "mockery") {
		return args
	}
	line = strings.TrimPrefix(line, "mockery")
	line = strings.TrimSpace(line)

	fields := strings.Fields(line)
	for _, field := range fields {
		if !strings.HasPrefix(field, "--") {
			continue
		}
		field = strings.TrimPrefix(field, "--")

		parts := strings.SplitN(field, "=", 2)
		key := parts[0]
		var val string
		if len(parts) == 2 {
			val = parts[1]
		} else {
			val = "true" // флаги без значения = true
		}
		args[key] = val
	}
	return args
}

func applyArg(iface *InterfaceConfig, args map[string]string, key string, setter func(string)) {
	if v, ok := args[key]; ok {
		setter(v)
	}
}

func applyBoolArg(iface *InterfaceConfig, args map[string]string, key string, setter func(bool)) {
	if v, ok := args[key]; ok {
		b, err := strconv.ParseBool(v)
		if err == nil {
			setter(b)
		} else if v == "" || v == "true" {
			setter(true)
		} else {
			setter(false)
		}
	}
}
