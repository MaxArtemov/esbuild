package cache

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/evanw/esbuild/internal/logger"
	"github.com/evanw/esbuild/internal/runtime"
)

func GetCacheFromDisk() (error, *CacheSet) {
	caches := MakeCacheSet()
	var cacheDir = "/Users/maxa/projects/esbuild/cache_jsons"
	cacheSet, cacheReadError := LoadCacheFromDir(cacheDir, caches)

	return cacheReadError, cacheSet
}

// This is a cache of the parsed contents of a set of files. The idea is to be
// able to reuse the results of parsing between builds and make subsequent
// builds faster by avoiding redundant parsing work. This only works if:
//
//   - The AST information in the cache must be considered immutable. There is
//     no way to enforce this in Go, but please be disciplined about this. The
//     ASTs are shared in between builds. Any information that must be mutated
//     in the AST during a build must be done on a shallow clone of the data if
//     the mutation happens after parsing (i.e. a clone that clones everything
//     that will be mutated and shares only the parts that won't be mutated).
//
//   - The information in the cache must not depend at all on the contents of
//     any file other than the file being cached. Invalidating an entry in the
//     cache does not also invalidate any entries that depend on that file, so
//     caching information that depends on other files can result in incorrect
//     results due to reusing stale data. For example, do not "bake in" some
//     value imported from another file.
//
//   - Cached ASTs must only be reused if the parsing options are identical
//     between builds. For example, it would be bad if the AST parser depended
//     on options inherited from a nearby "package.json" file but those options
//     were not part of the cache key. Then the cached AST could incorrectly be
//     reused even if the contents of that "package.json" file have changed.
type CacheSet struct {
	FSCache          FSCache
	CSSCache         CSSCache
	JSONCache        JSONCache
	JSCache          JSCache
	SourceIndexCache SourceIndexCache
}

func (cacheSet *CacheSet) AddJsEntry(cacheEntry *jsCacheEntry) {
	cacheSet.JSCache.mutex.Lock()
	defer cacheSet.JSCache.mutex.Unlock()
	cacheSet.JSCache.entries[cacheEntry.source.KeyPath] = cacheEntry
}

func (cacheSet *CacheSet) OverrideJsCacheEntries(jsCacheEntries *JSCacheEntries) {
	cacheSet.JSCache.entries = jsCacheEntries.Entries
}

func MakeCacheSet() *CacheSet {
	return &CacheSet{
		SourceIndexCache: SourceIndexCache{
			globEntries:     make(map[uint64]uint32),
			entries:         make(map[sourceIndexKey]uint32),
			nextSourceIndex: runtime.SourceIndex + 1,
		},
		FSCache: FSCache{
			entries: make(map[string]*fsEntry),
		},
		CSSCache: CSSCache{
			entries: make(map[logger.Path]*cssCacheEntry),
		},
		JSONCache: JSONCache{
			entries: make(map[logger.Path]*jsonCacheEntry),
		},
		JSCache: JSCache{
			entries: make(map[logger.Path]*jsCacheEntry),
		},
	}
}

type SourceIndexCache struct {
	globEntries     map[uint64]uint32
	entries         map[sourceIndexKey]uint32
	mutex           sync.Mutex
	nextSourceIndex uint32
}
type SourceIndexCacheSerialized struct {
	GlobEntries     map[uint64]uint32
	Entries         map[string]uint32
	NextSourceIndex uint32
}

func (srcIdxCache *SourceIndexCache) GetFromDisk() ([]byte, error) {
	filePath := "/Users/maxa/projects/esbuild/index_cache/source_index_cache.json"
	contents, readFileErr := os.ReadFile(filePath)
	if readFileErr != nil {
		panic(readFileErr)
	}
	serialized := SourceIndexCacheSerialized{}
	err := json.Unmarshal(contents, &serialized)
	if err != nil {
		fmt.Println("Error unmarshalling cache entry", err)
		panic(err)
	}
	srcIdxCache.Deserialize(serialized)
	return contents, readFileErr
}

func (srcIdxCache *SourceIndexCache) Serialize() SourceIndexCacheSerialized {
	serialized := SourceIndexCacheSerialized{}
	serialized.NextSourceIndex = srcIdxCache.nextSourceIndex
	serialized.Entries = make(map[string]uint32)
	for key, value := range srcIdxCache.entries {
		serialized.Entries[key.ToString()] = value
	}
	serialized.GlobEntries = srcIdxCache.globEntries
	return serialized
}

func (srcIdxCache *SourceIndexCache) Deserialize(serialized SourceIndexCacheSerialized) {
	srcIdxCache.nextSourceIndex = serialized.NextSourceIndex
	srcIdxCache.entries = make(map[sourceIndexKey]uint32)
	for key, value := range serialized.Entries {
		srcIdxCache.entries[SrcIdxKeyFromString(key)] = value
	}
	srcIdxCache.globEntries = serialized.GlobEntries
}

func (srcIdxCache *SourceIndexCache) Persist() error {
	serialized := srcIdxCache.Serialize()

	content, err := json.Marshal(serialized)
	if err != nil {
		panic(err)
	}
	filePath := "/Users/maxa/projects/esbuild/index_cache/source_index_cache.json"
	if len(content) != 0 {
		err2 := os.WriteFile(filePath, content, 0644)
		if err2 != nil {
			fmt.Println("Error writing cache to disk", err2)

		}
	} else {
		fmt.Println("Error marshalling cache entry, Empty entry serialzied ({})", serialized)
		return errors.New("error marshalling cache entry, Empty entry serialzied")
	}
	return nil
}

type SourceIndexKind uint8

const (
	SourceIndexNormal SourceIndexKind = iota
	SourceIndexJSStubForCSS
)

type sourceIndexKey struct {
	path logger.Path
	kind SourceIndexKind
}

func (s *sourceIndexKey) ToString() string {
	pathStr := s.path.ToString()
	return fmt.Sprintf("%s %d", pathStr, s.kind)
}

func SrcIdxKeyFromString(str string) sourceIndexKey {
	var pathStr string
	var kind SourceIndexKind

	_, err1 := fmt.Sscanf(str, "%s %d", &pathStr, &kind)
	if err1 != nil {
		panic(err1)
	}
	path, err := logger.PathFromString(pathStr)

	if err != nil {
		panic(err)
	}
	return sourceIndexKey{path: *path, kind: kind}
}

func (c *SourceIndexCache) LenHint() uint32 {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	// Add some extra room at the end for a new file or two without reallocating
	const someExtraRoom = 16
	return c.nextSourceIndex + someExtraRoom
}

func (c *SourceIndexCache) Get(path logger.Path, kind SourceIndexKind) uint32 {
	key := sourceIndexKey{path: path, kind: kind}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if sourceIndex, ok := c.entries[key]; ok {
		return sourceIndex
	}
	sourceIndex := c.nextSourceIndex
	c.nextSourceIndex++
	c.entries[key] = sourceIndex
	return sourceIndex
}

func (c *SourceIndexCache) GetGlob(parentSourceIndex uint32, globIndex uint32) uint32 {
	key := (uint64(parentSourceIndex) << 32) | uint64(globIndex)
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if sourceIndex, ok := c.globEntries[key]; ok {
		return sourceIndex
	}
	sourceIndex := c.nextSourceIndex
	c.nextSourceIndex++
	c.globEntries[key] = sourceIndex
	return sourceIndex
}
