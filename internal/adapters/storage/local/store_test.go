package local

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/cineko-org/client/internal/application"
	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/v3/gen/go/cineko/common"
	seatmappb "github.com/cineko-org/contracts/v3/gen/go/cineko/seatmap"
)

func TestStorePersistsPosterSeatMapAndResourceAcrossRestart(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	store, err := Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}

	movieID, mediaType := "movie_0123456789abcdef0123456789abcdef", "image/jpeg"
	posterData := []byte("poster")
	digest := sha256.Sum256(posterData)
	contentHash := hex.EncodeToString(digest[:])
	providerID, providerName := "cgv", "CGV"
	movieTitle := "포스터 테스트"
	if err := store.PutCatalogSnapshot(t.Context(), catalogpb.CatalogSnapshot_builder{
		Provider: catalogpb.Provider_builder{Id: &providerID, Name: &providerName}.Build(),
		Movies: []*catalogpb.Movie{catalogpb.Movie_builder{
			Id: &movieID, ProviderId: &providerID, Title: &movieTitle,
		}.Build()},
		Posters: []*catalogpb.MoviePoster{catalogpb.MoviePoster_builder{
			MovieId: &movieID, MediaType: &mediaType, Data: posterData, ContentHash: &contentHash,
		}.Build()},
	}.Build()); err != nil {
		t.Fatal(err)
	}

	auditoriumID, layoutHash := "auditorium-1", "sha256:layout"
	if err := store.PutSeatMap(t.Context(), seatmappb.Snapshot_builder{
		AuditoriumId: &auditoriumID, LayoutHash: &layoutHash,
	}.Build()); err != nil {
		t.Fatal(err)
	}

	presetID, userID, name := "preset-1", localUserID, "용산 중앙"
	revision := int64(0)
	preset := clientpb.Resource_builder{
		Identity: commonpb.ResourceIdentity_builder{Id: &presetID, Revision: &revision}.Build(),
		Preset:   clientpb.Preset_builder{Id: &presetID, UserId: &userID, Name: &name}.Build(),
	}.Build()
	if err := store.PutPreset(t.Context(), preset); err != nil {
		t.Fatal(err)
	}
	if preset.GetIdentity().GetRevision() != 1 {
		t.Fatalf("stored revision = %d, want 1", preset.GetIdentity().GetRevision())
	}

	reopened, err := Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	poster, err := reopened.GetMoviePoster(t.Context(), movieID)
	if err != nil {
		t.Fatal(err)
	}
	if string(poster.Data) != "poster" || poster.ContentHash != contentHash {
		t.Fatalf("reopened poster = %#v", poster)
	}
	catalog, err := reopened.GetCatalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	wantPosterURL := "/v1/catalog/posters/" + movieID + "?v=" + contentHash
	if len(catalog.GetMovies()) != 1 || catalog.GetMovies()[0].GetPosterUrl() != wantPosterURL {
		t.Fatalf("reopened movie poster URL = %q, want %q", catalog.GetMovies()[0].GetPosterUrl(), wantPosterURL)
	}
	seatMap, err := reopened.GetSeatMap(t.Context(), auditoriumID)
	if err != nil {
		t.Fatal(err)
	}
	if seatMap.GetLayoutHash() != layoutHash {
		t.Fatalf("reopened layout hash = %q", seatMap.GetLayoutHash())
	}
	reopenedPreset, err := reopened.GetPreset(t.Context(), presetID)
	if err != nil {
		t.Fatal(err)
	}
	if reopenedPreset.GetPreset().GetName() != name || reopenedPreset.GetIdentity().GetRevision() != 1 {
		t.Fatalf("reopened preset = %v", reopenedPreset)
	}
}

func TestUpsertMoviePreservesLocalPosterURL(t *testing.T) {
	t.Parallel()
	movieID, providerID, originalTitle := "movie_0123456789abcdef0123456789abcdef", "cgv", "원래 제목"
	posterURL := "/v1/catalog/posters/" + movieID + "?v=" + strings.Repeat("a", 64)
	index := catalogpb.CatalogIndex_builder{Movies: []*catalogpb.Movie{catalogpb.Movie_builder{
		Id: &movieID, ProviderId: &providerID, Title: &originalTitle, PosterUrl: &posterURL,
	}.Build()}}.Build()
	updatedTitle := "일정에서 갱신된 제목"
	upsertMovie(index, catalogpb.Movie_builder{Id: &movieID, ProviderId: &providerID, Title: &updatedTitle}.Build())
	if got := index.GetMovies()[0]; got.GetTitle() != updatedTitle || got.GetPosterUrl() != posterURL {
		t.Fatalf("updated movie = %v", got)
	}
}

func TestStoreRejectsStaleResourceRevision(t *testing.T) {
	t.Parallel()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id, userID, name := "preset-1", localUserID, "original"
	revision := int64(0)
	resource := clientpb.Resource_builder{
		Identity: commonpb.ResourceIdentity_builder{Id: &id, Revision: &revision}.Build(),
		Preset:   clientpb.Preset_builder{Id: &id, UserId: &userID, Name: &name}.Build(),
	}.Build()
	if err := store.PutPreset(t.Context(), resource); err != nil {
		t.Fatal(err)
	}
	resource.GetIdentity().SetRevision(0)
	if err := store.PutPreset(t.Context(), resource); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("stale write error = %v, want conflict", err)
	}
}
