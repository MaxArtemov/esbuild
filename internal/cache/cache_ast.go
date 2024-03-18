package cache

import (
	"encoding/json"
	"fmt"
	"io/fs"
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

func parseCacheEntryFromJson(serializedCacheEntry SerializedCacheEntry) (*jsCacheEntry, error) {
	desAst, err := serializedCacheEntry.Ast.DeserializeFromJson()
	if err != nil {
		fmt.Println("FUNKY ERROR IN PARSE CACHE ENTRY FROM JSON", err)
		return nil, err
	}
	var cacheEntry jsCacheEntry
	var src logger.Source
	src, err = src.SourceFromString(serializedCacheEntry.Source)
	if err != nil {
		fmt.Println("Error creating source from string", err)
		return nil, err
	}
	cacheEntry.ast = desAst
	cacheEntry.source = src
	cacheEntry.ok = serializedCacheEntry.Ok
	cacheEntry.msgs = []logger.Msg{}

	return &cacheEntry, nil
}

func (c *jsCacheEntry) GetSingleCacheEntryFromDisk(entry *jsCacheEntry) *jsCacheEntry {
	filePath := entry.getJsonPath()
	contents, readFileErr := os.ReadFile(filePath)
	if readFileErr != nil {
		fmt.Println("Error reading file info from cache", readFileErr, filePath)
		panic(readFileErr)
		// return cacheSet, readFileErr
	}
	var serializedCacheEntry SerializedCacheEntry
	parseErr := json.Unmarshal(contents, &serializedCacheEntry)
	if parseErr != nil {
		fmt.Println("Error parsing cache entry from json", parseErr)
		panic(parseErr)

	}
	cacheEntry, err := parseCacheEntryFromJson(serializedCacheEntry)
	if err != nil {
		fmt.Println("Error parsing cache entry from json", err)
		panic(err)
	}

	return cacheEntry
}

// Load the cache from a file
func LoadCacheFromDir(cacheDir string, cacheSet *CacheSet) (*CacheSet, error) {
	fmt.Println("Load cache from dir and fill initial cache!", cacheDir)
	cacheFiles, err := os.ReadDir(cacheDir)
	if err != nil {
		return nil, err
	}

	var sourceIndexCache SourceIndexCache
	_, err2 := sourceIndexCache.GetFromDisk()

	// fmt.Println("Source index cache contents Filled, filling jsons", string(contents))

	if err2 != nil {
		panic(err2)
	}

	var wg sync.WaitGroup

	for _, file := range cacheFiles {
		wg.Add(1)
		go func(file fs.DirEntry) {
			defer wg.Done()
			// fmt.Println("Load cache from dir and fill initial cache!", file.Name())
			var serializedCacheEntry SerializedCacheEntry
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
					panic(readFileErr)
					// return cacheSet, readFileErr
				}
				parseErr := json.Unmarshal(contents, &serializedCacheEntry)
				cacheEntry, err := parseCacheEntryFromJson(serializedCacheEntry)
				if err != nil {
					fmt.Println("Error parsing cache entry from json", err)
					panic(err)
				}
				cacheSet.AddJsEntry(cacheEntry)

				if parseErr != nil {
					fmt.Println("Parse errror (Unmarshal)", parseErr)
					panic(parseErr)
					// return cacheSet, parseErr
				}

			}
		}(file)
	}
	wg.Wait()
	cacheSet.SourceIndexCache.entries = sourceIndexCache.entries
	cacheSet.SourceIndexCache.globEntries = sourceIndexCache.globEntries
	cacheSet.SourceIndexCache.nextSourceIndex = sourceIndexCache.nextSourceIndex

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

func (s SerializedCacheEntry) ToCacheEntry() jsCacheEntry {
	return jsCacheEntry{}
}

func (c jsCacheEntry) MarshalJSON() ([]byte, error) {
	serializedAst := c.ast.SerializeForJson()
	cacheEntry := SerializedCacheEntry{
		Ast:    *serializedAst,
		Source: c.source.ToString(),
		Ok:     c.ok,
		Msgs:   []string{"first", "second", "third"},
	}
	content, err := json.Marshal(cacheEntry)
	if err != nil {
		fmt.Println("Error marshalling cache entry to json", err)
	}
	return content, err
}

func (c *jsCacheEntry) UnmarshalJSON(b []byte) error {
	// TODO: make desirailzation from json work here
	serializedCacheEntry := SerializedCacheEntry{}
	err := json.Unmarshal(b, &serializedCacheEntry)
	if err != nil {
		return err
	}

	cacheEntry, err := parseCacheEntryFromJson(serializedCacheEntry)

	if err != nil {
		fmt.Println("Error parsing cache entry from json", err)
		return err
	}
	*c = *cacheEntry
	return nil
}

func (c *jsCacheEntry) getJsonPath() string {
	contentHash := my_helpers.HashString(c.source.Contents + c.source.IdentifierName)
	// entryCacheKey := c.source.KeyPath.ToString()

	return "/Users/maxa/projects/esbuild/cache_jsons/" + contentHash + ".json"
}

func (c *JSCache) SetCacheEntry(entry *jsCacheEntry) {
	c.mutex.Lock()
	c.entries[entry.source.KeyPath] = entry
	c.mutex.Unlock()
	// TODO: Uncomment when persisting cache
	// entryPath := entry.source.KeyPath
	// go func(keyPath logger.Path, entryPath logger.Path) {
	// 	// Save the cache entry to a file
	// 	jsonPath := entry.getJsonPath()
	// 	// fmt.Println("Save cache entry to file", entryPath, jsonPath)
	// 	SaveCacheEntryToFile(c, jsonPath, entryPath)
	// }(entry.source.KeyPath, entryPath)
}

func (c *JSCache) GetCacheEntries() *JSCacheEntries {
	return &JSCacheEntries{
		Entries: c.entries,
	}
}

var (
	counterHit  = 0
	counterMiss = 0
)

func (c *JSCache) Parse(log logger.Log, source logger.Source, options js_parser.Options) (js_ast.AST, bool) {
	// Check the cache
	entry := func() *jsCacheEntry {
		c.mutex.Lock()
		defer c.mutex.Unlock()
		return c.entries[source.KeyPath]
	}()

	// Cache hit
	// TODO: this is the original check -
	// if entry != nil && entry.source == source && entry.options.Equal(&options)
	// { (including options) }
	// We remove the options as serializing it would take a lot of time and for a POC its redundant. cache will be shared in between
	// builds with different options which is incorrect but for the sake of POC we will ignore it.

	// entry.source.PrettyPath == source.PrettyPath && entry.source.Contents == source.Contents
	// entry.source == source
	// and then check how to update the index.
	if entry != nil && entry.source.PrettyPath == source.PrettyPath && entry.source.Contents == source.Contents {
		for _, msg := range entry.msgs {
			log.AddMsg(msg)
		}
		// counterHit++
		fmt.Println("Cache HIT :) Index:", source.Index, source.PrettyPath)
		// fmt.Println("Current run index", source.Index)
		// fmt.Println("Cached index", entry.source.Index)
		// Not sure it's needed, but looks like its generated per build and
		// if we want to persist cache it is essential to keep the index numbers consistent for a build
		// if entry.source.Index != source.Index {
		// 	fmt.Println("Switched index to", source.Index, "from", entry.source.Index)
		// 	entry.source = source
		// }
		return entry.ast, entry.ok
	}

	// Cache miss
	counterMiss++
	fmt.Println("Cache MISS :)", counterMiss, source.Index, source.PrettyPath)
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
