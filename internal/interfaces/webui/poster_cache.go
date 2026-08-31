package webui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/cineko-org/client/internal/application"
	"github.com/cineko-org/client/internal/logging"
)

const maximumCachedPosterBytes = 32 << 20

type posterCache struct {
	directory string
	source    application.MoviePosterRepository
	locks     sync.Map
}

type cachedPoster struct {
	mediaType   string
	contentHash string
	data        []byte
}

func newPosterCache(directory string, source application.MoviePosterRepository) (*posterCache, error) {
	directory = filepath.Clean(strings.TrimSpace(directory))
	if directory == "." || source == nil {
		return nil, errors.New("movie poster cache dependencies are incomplete")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create movie poster cache: %w", err)
	}
	return &posterCache{directory: directory, source: source}, nil
}

func (server *Server) moviePoster(writer http.ResponseWriter, request *http.Request) {
	movieID := strings.TrimSpace(request.PathValue("movieId"))
	version := strings.ToLower(strings.TrimSpace(request.URL.Query().Get("v")))
	if server.posterCache == nil {
		http.Error(writer, "movie poster cache unavailable", http.StatusServiceUnavailable)
		return
	}
	poster, err := server.posterCache.load(request.Context(), movieID, version)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, application.ErrNotFound) {
			status = http.StatusNotFound
		}
		logging.ErrorUnexpected(request.Context(), "poster.cache.failed", "poster_delivery", "load_cached_poster",
			"valid locally cached poster matching the catalog version", "poster could not be served", err,
			"movie_id", movieID, "poster_version", version, "status", status)
		http.Error(writer, "movie poster unavailable", status)
		return
	}
	etag := `"` + poster.contentHash + `"`
	writer.Header().Set("Content-Type", poster.mediaType)
	writer.Header().Set("ETag", etag)
	writer.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	if strings.TrimSpace(request.Header.Get("If-None-Match")) == etag {
		writer.WriteHeader(http.StatusNotModified)
		return
	}
	writer.Header().Set("Content-Length", fmt.Sprint(len(poster.data)))
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(poster.data)
}

func (cache *posterCache) load(ctx context.Context, movieID, version string) (*cachedPoster, error) {
	if !validMoviePosterID(movieID) || !validPosterHash(version) {
		return nil, errors.New("movie poster cache key is invalid")
	}
	key := movieID + ":" + version
	lockValue, _ := cache.locks.LoadOrStore(key, &sync.Mutex{})
	lock, ok := lockValue.(*sync.Mutex)
	if !ok {
		return nil, errors.New("movie poster cache lock is invalid")
	}
	lock.Lock()
	defer lock.Unlock()
	if poster, ok := cache.read(movieID, version); ok {
		logging.Debug(ctx, "poster.cache.hit", "event", "poster.cache.hit", "scenario", "poster_delivery",
			"operation", "load_cached_poster", "outcome", "succeeded",
			"movie_id", movieID, "poster_version", version, "bytes", len(poster.data))
		return poster, nil
	}
	logging.Debug(ctx, "poster.cache.miss", "event", "poster.cache.miss", "scenario", "poster_delivery",
		"operation", "load_cached_poster", "outcome", "cache_miss", "movie_id", movieID, "poster_version", version)
	downloaded, err := cache.source.GetMoviePoster(ctx, movieID)
	if err != nil {
		return nil, err
	}
	if downloaded == nil || downloaded.MovieID != movieID || downloaded.ContentHash != version ||
		!supportedCachedPosterMediaType(downloaded.MediaType) || len(downloaded.Data) == 0 || len(downloaded.Data) > maximumCachedPosterBytes {
		return nil, errors.New("cached movie poster does not match the catalog version")
	}
	digest := sha256.Sum256(downloaded.Data)
	if hex.EncodeToString(digest[:]) != downloaded.ContentHash {
		return nil, errors.New("cached movie poster content hash is invalid")
	}
	poster := &cachedPoster{
		mediaType: downloaded.MediaType, contentHash: downloaded.ContentHash, data: append([]byte(nil), downloaded.Data...),
	}
	if err := cache.write(movieID, poster); err != nil {
		return nil, err
	}
	logging.Info(ctx, "poster.cache.stored", "event", "poster.cache.stored", "scenario", "poster_delivery",
		"operation", "store_cached_poster", "outcome", "succeeded",
		"movie_id", movieID, "poster_version", version, "bytes", len(poster.data))
	return poster, nil
}

func (cache *posterCache) read(movieID, version string) (*cachedPoster, bool) {
	for mediaType, extension := range posterExtensions() {
		// #nosec G304,G703 -- load validates both fixed-format path components before this lookup.
		data, err := os.ReadFile(filepath.Join(cache.directory, movieID, version+extension))
		if err != nil || len(data) == 0 || len(data) > maximumCachedPosterBytes {
			continue
		}
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != version {
			continue
		}
		return &cachedPoster{mediaType: mediaType, contentHash: version, data: data}, true
	}
	return nil, false
}

func (cache *posterCache) write(movieID string, poster *cachedPoster) error {
	extension := posterExtensions()[poster.mediaType]
	directory := filepath.Join(cache.directory, movieID)
	if extension == "" {
		return errors.New("movie poster media type is unsupported")
	}
	// #nosec G703 -- movieID passed the fixed-format hex identifier validation in load.
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create movie poster cache directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".poster-*")
	if err != nil {
		return fmt.Errorf("create movie poster cache file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		// #nosec G703 -- temporaryPath is returned by os.CreateTemp in the validated cache directory.
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("protect movie poster cache file: %w", err)
	}
	if _, err := temporary.Write(poster.data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write movie poster cache file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("flush movie poster cache file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close movie poster cache file: %w", err)
	}
	// #nosec G703 -- contentHash is a verified lowercase SHA-256 and extension is an internal constant.
	if err := os.Rename(temporaryPath, filepath.Join(directory, poster.contentHash+extension)); err != nil {
		return fmt.Errorf("publish movie poster cache file: %w", err)
	}
	return nil
}

func posterExtensions() map[string]string {
	return map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp"}
}

func supportedCachedPosterMediaType(value string) bool {
	_, ok := posterExtensions()[strings.ToLower(strings.TrimSpace(value))]
	return ok
}

func validMoviePosterID(value string) bool {
	if !strings.HasPrefix(value, "movie_") || len(value) != len("movie_")+32 {
		return false
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(value, "movie_"))
	return err == nil && len(decoded) == 16
}

func validPosterHash(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}
