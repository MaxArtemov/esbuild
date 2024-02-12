package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/evanw/esbuild/internal/css_ast"
	"github.com/evanw/esbuild/internal/css_parser"
	"github.com/evanw/esbuild/internal/js_ast"
	"github.com/evanw/esbuild/internal/js_parser"
	"github.com/evanw/esbuild/internal/logger"
	"github.com/evanw/esbuild/internal/my_helpers"
)

// This cache intends to avoid unnecessarily re-parsing files in subsequent
// builds. For a given path, parsing can be avoided if the contents of the file
// and the options for the parser are the same as last time. Even if the
// contents of the file are the same, the options for the parser may have
// changed if they depend on some other file ("package.json" for example).
//
// This cache checks if the file contents have changed even though we have
// the ability to detect if a file has changed on the file system by reading
// its metadata. First of all, if the file contents are cached then they should
// be the same pointer, which makes the comparison trivial. Also we want to
// cache the AST for plugins in the common case that the plugin output stays
// the same.

////////////////////////////////////////////////////////////////////////////////
// CSS

type CSSCache struct {
	entries map[logger.Path]*cssCacheEntry
	mutex   sync.Mutex
}

type cssCacheEntry struct {
	source  logger.Source
	msgs    []logger.Msg
	ast     css_ast.AST
	options css_parser.Options
}

func (c *CSSCache) Parse(log logger.Log, source logger.Source, options css_parser.Options) css_ast.AST {
	// Check the cache
	entry := func() *cssCacheEntry {
		c.mutex.Lock()
		defer c.mutex.Unlock()
		return c.entries[source.KeyPath]
	}()

	// Cache hit
	if entry != nil && entry.source == source && entry.options.Equal(&options) {
		for _, msg := range entry.msgs {
			log.AddMsg(msg)
		}
		return entry.ast
	}

	// Cache miss
	tempLog := logger.NewDeferLog(logger.DeferLogAll, log.Overrides)
	ast := css_parser.Parse(tempLog, source, options)
	msgs := tempLog.Done()
	for _, msg := range msgs {
		log.AddMsg(msg)
	}

	// Create the cache entry
	entry = &cssCacheEntry{
		source:  source,
		options: options,
		ast:     ast,
		msgs:    msgs,
	}

	// Save for next time
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.entries[source.KeyPath] = entry
	return ast
}

////////////////////////////////////////////////////////////////////////////////
// JSON

type JSONCache struct {
	entries map[logger.Path]*jsonCacheEntry
	mutex   sync.Mutex
}

type jsonCacheEntry struct {
	expr    js_ast.Expr
	msgs    []logger.Msg
	source  logger.Source
	options js_parser.JSONOptions
	ok      bool
}

func (c *JSONCache) Parse(log logger.Log, source logger.Source, options js_parser.JSONOptions) (js_ast.Expr, bool) {
	// Check the cache
	entry := func() *jsonCacheEntry {
		c.mutex.Lock()
		defer c.mutex.Unlock()
		return c.entries[source.KeyPath]
	}()

	// Cache hit
	if entry != nil && entry.source == source && entry.options == options {
		for _, msg := range entry.msgs {
			log.AddMsg(msg)
		}
		return entry.expr, entry.ok
	}

	// Cache miss
	tempLog := logger.NewDeferLog(logger.DeferLogAll, log.Overrides)
	expr, ok := js_parser.ParseJSON(tempLog, source, options)
	msgs := tempLog.Done()
	for _, msg := range msgs {
		log.AddMsg(msg)
	}

	// Create the cache entry
	entry = &jsonCacheEntry{
		source:  source,
		options: options,
		expr:    expr,
		ok:      ok,
		msgs:    msgs,
	}

	// Save for next time
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.entries[source.KeyPath] = entry
	return expr, ok
}

////////////////////////////////////////////////////////////////////////////////
// JS

type JSCache struct {
	entries map[logger.Path]*jsCacheEntry
	mutex   sync.Mutex
}
type JSCacheEntries struct {
	Entries map[logger.Path]*jsCacheEntry
}

// Save the cache to a file
func SaveCacheEntryToFile(cache *JSCache, filePath string, entryPath logger.Path) error {
	// TODO: dont use mutex for whole cache but just for relevant entry
	cache.mutex.Lock()
	defer cache.mutex.Unlock()
	entry := cache.entries[entryPath]
	// entries := cache.GetCacheEntries()
	// entry := entries.Entries[entryPath]

	data3, err3 := json.Marshal(*entry)

	if err3 != nil {
		return err3
	}

	if len(data3) != 0 {
		return os.WriteFile(filePath, data3, 0644)
	} else {
		fmt.Println("Error marshalling cache entry, Empty entry serialzied ({})", entryPath)
		return error(nil)
	}
}

// Load the cache from a file
func LoadCacheFromDir(cacheDir string, cacheSet *CacheSet) (*CacheSet, error) {
	cacheFiles, err := os.ReadDir(cacheDir)
	if err != nil {
		return nil, err
	}

	for _, file := range cacheFiles {
		var cacheEntry jsCacheEntry
		fileInfo, err := file.Info()

		if err != nil {
			fmt.Println("Error getting file infos", fileInfo)
		}
		if fileInfo.Mode().IsRegular() {
			// Build the full path to the file
			filePath := filepath.Join(cacheDir, fileInfo.Name())
			// entryCacheKey := strings.Split(filePath, "---")[0]
			contents, readFileErr := os.ReadFile(filePath)
			if readFileErr != nil {
				fmt.Println("Error reading file info from cache", readFileErr, fileInfo)
				return cacheSet, readFileErr
			}

			parseErr := json.Unmarshal(contents, &cacheEntry)
			cacheSet.AddJsEntry(&cacheEntry)

			if parseErr != nil {
				fmt.Println("Parse errror (Unmarshal)", parseErr)
				return cacheSet, parseErr
			}

		}
	}

	return cacheSet, nil
}

type jsCacheEntry struct {
	source  logger.Source
	msgs    []logger.Msg
	options js_parser.Options
	ast     js_ast.AST
	ok      bool
}

type SerializedCacheEntry struct {
	Ast    js_ast.SerializedAST
	Source string
	Ok     bool
	Msgs   []string
}

func (c jsCacheEntry) MarshalJSON() ([]byte, error) {
	serializedAst := c.ast.SerializeForJson()
	cacheEntry := SerializedCacheEntry{
		Ast:    *serializedAst,
		Source: c.source.ToString(),
		Ok:     c.ok,
		Msgs:   []string{"first", "second", "third"},
	}
	fmt.Println("serilaized cacheEntry", cacheEntry)
	return json.Marshal(cacheEntry)
}

func (c *jsCacheEntry) getJsonPath() string {
	contentHash := my_helpers.HashString(c.source.Contents)
	// entryCacheKey := c.source.KeyPath.ToString()

	return "/Users/maxa/projects/esbuild/cache_jsons/" + contentHash + ".json"
}

func (c *jsCacheEntry) creteJsonPath(entryCacheKey string) string {
	contentHash := my_helpers.HashString(c.source.Contents)
	// entryCacheKey
	return "/Users/maxa/projects/esbuild/cache_jsons/" + contentHash + ".json"
}

func (c *JSCache) SetCacheEntry(entry *jsCacheEntry) {
	c.mutex.Lock()
	c.entries[entry.source.KeyPath] = entry
	entryPath := entry.source.KeyPath
	defer c.mutex.Unlock()
	go func(keyPath logger.Path, entryPath logger.Path) {
		// Save the cache entry to a file
		jsonPath := entry.getJsonPath()
		fmt.Println("jsonPath", jsonPath)
		SaveCacheEntryToFile(c, jsonPath, entryPath)
	}(entry.source.KeyPath, entryPath)
}

func (c *JSCache) GetCacheEntries() *JSCacheEntries {
	return &JSCacheEntries{
		Entries: c.entries,
	}
}

func (c *JSCache) Parse(log logger.Log, source logger.Source, options js_parser.Options) (js_ast.AST, bool) {
	// Check the cache
	entry := func() *jsCacheEntry {
		c.mutex.Lock()
		defer c.mutex.Unlock()
		return c.entries[source.KeyPath]
	}()

	// Cache hit
	if entry != nil && entry.source == source && entry.options.Equal(&options) {
		for _, msg := range entry.msgs {
			log.AddMsg(msg)
		}
		return entry.ast, entry.ok
	}

	// Cache miss
	tempLog := logger.NewDeferLog(logger.DeferLogAll, log.Overrides)
	ast, ok := js_parser.Parse(tempLog, source, options)
	msgs := tempLog.Done()
	for _, msg := range msgs {
		log.AddMsg(msg)
	}

	// Create the cache entry
	entry = &jsCacheEntry{
		source:  source,
		options: options,
		ast:     ast,
		ok:      ok,
		msgs:    msgs,
	}

	// Save for next time

	// c.mutex.Lock() --------> moved lock to setCacheEntry
	// defer c.mutex.Unlock() --------> moved unlock to setCacheEntry
	// Set entry to cache through method instead of direct access to also save to json
	// c.entries[source.KeyPath] = entry
	c.SetCacheEntry(entry)
	return ast, ok
}
