package webui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/cineko-org/client/internal/application"
)

type posterSourceFake struct {
	poster *application.MoviePoster
	calls  int
}

func (source *posterSourceFake) GetMoviePoster(context.Context, string) (*application.MoviePoster, error) {
	source.calls++
	copy := *source.poster
	copy.Data = append([]byte(nil), source.poster.Data...)
	return &copy, nil
}

func TestPosterCacheDownloadsOnceThenUsesDisk(t *testing.T) {
	data := []byte("browser-captured-poster")
	digest := sha256.Sum256(data)
	version := hex.EncodeToString(digest[:])
	movieID := "movie_" + "a1b2c3d4e5f60718293a4b5c6d7e8f90"
	source := &posterSourceFake{poster: &application.MoviePoster{
		MovieID: movieID, MediaType: "image/jpeg", ContentHash: version, Data: data,
	}}
	cache, err := newPosterCache(t.TempDir(), source)
	if err != nil {
		t.Fatal(err)
	}
	first, err := cache.load(t.Context(), movieID, version)
	if err != nil {
		t.Fatal(err)
	}
	second, err := cache.load(t.Context(), movieID, version)
	if err != nil {
		t.Fatal(err)
	}
	if source.calls != 1 || string(first.data) != string(data) || string(second.data) != string(data) {
		t.Fatalf("poster cache calls = %d, first=%q second=%q", source.calls, first.data, second.data)
	}
}

func TestPosterCacheStoresObservedLargeCGVPoster(t *testing.T) {
	data := make([]byte, 12_118_780)
	for index := range data {
		data[index] = byte(index)
	}
	digest := sha256.Sum256(data)
	version := hex.EncodeToString(digest[:])
	movieID := "movie_" + "a1b2c3d4e5f60718293a4b5c6d7e8f90"
	source := &posterSourceFake{poster: &application.MoviePoster{
		MovieID: movieID, MediaType: "image/jpeg", ContentHash: version, Data: data,
	}}
	cache, err := newPosterCache(t.TempDir(), source)
	if err != nil {
		t.Fatal(err)
	}
	poster, err := cache.load(t.Context(), movieID, version)
	if err != nil {
		t.Fatal(err)
	}
	if len(poster.data) != len(data) {
		t.Fatalf("cached poster bytes = %d, want %d", len(poster.data), len(data))
	}
}

func TestPosterCacheRejectsUnversionedAndMismatchedAssets(t *testing.T) {
	movieID := "movie_" + "a1b2c3d4e5f60718293a4b5c6d7e8f90"
	data := []byte("poster")
	digest := sha256.Sum256(data)
	source := &posterSourceFake{poster: &application.MoviePoster{
		MovieID: movieID, MediaType: "image/jpeg", ContentHash: hex.EncodeToString(digest[:]), Data: data,
	}}
	cache, err := newPosterCache(t.TempDir(), source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cache.load(t.Context(), movieID, ""); err == nil {
		t.Fatal("unversioned poster cache lookup succeeded")
	}
	if _, err := cache.load(t.Context(), movieID, strings.Repeat("a", 64)); err == nil {
		t.Fatal("mismatched poster cache lookup succeeded")
	}
}
