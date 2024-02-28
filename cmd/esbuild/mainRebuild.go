package main

import (
	"fmt"

	"github.com/evanw/esbuild/internal/cache"
	"github.com/evanw/esbuild/internal/helpers"
	"github.com/evanw/esbuild/internal/logger"
	"github.com/evanw/esbuild/pkg/api"
)

func main() {

	log := logger.NewStderrLog(logger.OutputOptions{
		IncludeSource: true,
		MessageLimit:  500,
		Color:         2,
	})

	timer := &helpers.Timer{}
	// actualEntry := "/Users/maxa/projects/bundler-poc/src/code/materialUsed.jsx"
	// emptyEntry := "/Users/maxa/projects/bundler-poc/src/code/materialUsedEmpty.jsx"
	simpleEntry := "/Users/maxa/projects/bundler-poc/src/code/materialUsed.jsx"

	EntryPoints := []string{
		// "/Users/maxa/projects/bundler-poc/src/code/full-mui.jsx",
		// "/Users/maxa/projects/bundler-poc/src/code/mui-icons.jsx",
		// emptyEntry,
		simpleEntry,
	}

	timer.Begin("read-cache")
	cacheError, cacheSet := cache.GetCacheFromDisk()
	if cacheError != nil {
		fmt.Println("Error reading cache from disk", cacheError)
	}
	timer.End("read-cache")

	timer.Begin("create-context")
	myContext, err := api.Context(api.BuildOptions{
		Caches:            cacheSet,
		EntryPoints:       EntryPoints,
		EntryNames:        "[dir]/[name]-[hash]",
		Bundle:            true,
		MinifyWhitespace:  true,
		MinifyIdentifiers: true,
		MinifySyntax:      true,
		TreeShaking:       api.TreeShakingTrue,
		Format:            api.FormatESModule,
		JSX:               api.JSXAutomatic,
		// Outfile:           "dist/app.js",
		External: []string{"react", "react-dom"},
		Outdir:   "./dist",
		// Write:    true,
	})
	timer.End("create-context")

	if err != nil {
		panic(err)
	}

	timer.Begin("first-rebuild")
	result := myContext.Rebuild()
	timer.End("first-rebuild")

	for _, err := range result.Errors {
		fmt.Println("error from first build", err)
	}

	fmt.Println("First Rebuild done")

	// timer.Begin("replace file contents")
	// my_helpers.ReplaceFileContents(emptyEntry, actualEntry)
	// timer.End("replace file contents")

	timer.Begin("second-rebuild")
	result2 := myContext.Rebuild()
	timer.End("second-rebuild")

	for _, err := range result2.Errors {
		fmt.Println("error from second build", err)
	}

	timer.Log(log)
}
