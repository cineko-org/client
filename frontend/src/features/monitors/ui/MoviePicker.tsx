import { useState } from 'react';
import { Box, Image, Stack, Text, UnstyledButton } from '@mantine/core';
import type { Movie } from '../../../api/proto';
import { orderedCatalogMovies } from '../model';

export interface MoviePickerProps {
  movies: Movie[];
  value: string;
  onChange: (movie: Movie) => void;
}

const posterVersionPattern = /^[0-9a-f]{64}$/;

export function moviePosterSource(movie: Movie): string {
  if (!movie.posterUrl) return '';
  try {
    const parsed = new URL(movie.posterUrl, 'https://cineko.local');
    const expectedPath = `/v1/catalog/posters/${encodeURIComponent(movie.id)}`;
    const version = parsed.searchParams.get('v') || '';
    if (parsed.origin !== 'https://cineko.local' || parsed.pathname !== expectedPath || !posterVersionPattern.test(version)) return '';
    return `/api/catalog/posters/${encodeURIComponent(movie.id)}?v=${version}`;
  } catch {
    return '';
  }
}

function MoviePoster({ movie }: { movie: Movie }) {
  const source = moviePosterSource(movie);
  const [failedSource, setFailedSource] = useState('');
  return (
    <Box bg="dark.7" w="100%" style={{ aspectRatio: '7 / 10', overflow: 'hidden' }}>
      {source && source !== failedSource ? (
        <Image src={source} alt={`${movie.title} 포스터`} w="100%" h="100%" fit="contain" onError={() => setFailedSource(source)} />
      ) : (
        <Box display="flex" w="100%" h="100%" style={{ alignItems: 'center', justifyContent: 'center' }}>
          <Text size="xs" c="dimmed">포스터 준비 중</Text>
        </Box>
      )}
    </Box>
  );
}

export function MoviePicker({ movies, value, onChange }: MoviePickerProps) {
  return (
    <Stack gap="xs">
      <Text fw={600}>영화</Text>
      <Box
        style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(auto-fill, minmax(9rem, 11rem))',
          gap: 'var(--mantine-spacing-sm)',
          justifyContent: 'start',
        }}
      >
        {orderedCatalogMovies(movies).map((movie) => {
          const selected = movie.id === value;
          return (
            <UnstyledButton
              key={movie.id}
              aria-pressed={selected}
              aria-label={`${movie.title} 선택`}
              onClick={() => onChange(movie)}
              style={{
                outline: selected ? '2px solid var(--mantine-color-red-6)' : '1px solid var(--mantine-color-dark-4)',
                display: 'flex', flexDirection: 'column', width: '100%', height: '100%', overflow: 'hidden',
              }}
            >
              <MoviePoster movie={movie} />
              <Box p="sm" w="100%" style={{ minHeight: '4.5rem' }}>
                <Text
                  fw={selected ? 700 : 500}
                  size="sm"
                  lh={1.35}
                  lineClamp={2}
                  style={{ overflowWrap: 'anywhere', textAlign: 'left' }}
                >
                  {movie.title}
                </Text>
              </Box>
            </UnstyledButton>
          );
        })}
      </Box>
    </Stack>
  );
}
