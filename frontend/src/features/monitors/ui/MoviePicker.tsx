import { Box, Image, SimpleGrid, Stack, Text, UnstyledButton } from '@mantine/core';
import type { CatalogMovie } from '../../../api/types';
import { orderedCatalogMovies } from '../model';

export interface MoviePickerProps {
  movies: CatalogMovie[];
  value: string;
  onChange: (movie: CatalogMovie) => void;
}

export function MoviePicker({ movies, value, onChange }: MoviePickerProps) {
  return (
    <Stack gap="xs">
      <Text fw={600}>영화</Text>
      <SimpleGrid cols={{ base: 2, sm: 3, md: 4, xl: 5 }} spacing="sm">
        {orderedCatalogMovies(movies).map((movie) => {
          const selected = movie.id === value;
          return (
            <UnstyledButton
              key={movie.id}
              aria-pressed={selected}
              aria-label={`${movie.title} 선택`}
              onClick={() => onChange(movie)}
              style={{ outline: selected ? '2px solid var(--mantine-color-red-6)' : '1px solid var(--mantine-color-dark-4)' }}
            >
              {movie.posterUrl ? (
                <Image src={movie.posterUrl} alt="" w="100%" fit="cover" fallbackSrc="" style={{ aspectRatio: '2 / 3' }} />
              ) : (
                <Box bg="dark.7" display="flex" style={{ aspectRatio: '2 / 3', alignItems: 'center', justifyContent: 'center' }}>
                  <Text size="xs" c="dimmed">포스터 준비 중</Text>
                </Box>
              )}
              <Text fw={selected ? 700 : 500} size="sm" p="sm" lineClamp={2}>{movie.title}</Text>
            </UnstyledButton>
          );
        })}
      </SimpleGrid>
    </Stack>
  );
}
