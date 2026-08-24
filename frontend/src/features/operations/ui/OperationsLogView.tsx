import { Alert, Badge, Box, Code, Group, SegmentedControl, SimpleGrid, Stack, Text } from '@mantine/core';
import { SecondaryButton } from '../../../components/core/Actions';
import { Metric } from '../../../components/core/Metric';
import { EmptyState, Section } from '../../../components/core/Section';
import {
	formatLogTime, scenarioLabel,
	type OperationsLogSnapshot, type OperationsMinimumLevel,
} from '../model';

export interface OperationsLogViewProps {
	minimumLevel: OperationsMinimumLevel;
	snapshot: OperationsLogSnapshot;
	loading: boolean;
	error: string;
	onMinimumLevelChange: (value: OperationsMinimumLevel) => void;
	onReload: () => void;
}

function details(fields: Record<string, unknown> | undefined): string {
	if (!fields || Object.keys(fields).length === 0) return '';
	return JSON.stringify(fields, null, 2);
}

export function OperationsLogView({ minimumLevel, snapshot, loading, error, onMinimumLevelChange, onReload }: OperationsLogViewProps) {
	return (
		<Stack gap="xl">
			<Group justify="space-between" align="flex-end" wrap="wrap">
				<SegmentedControl
					value={minimumLevel}
					onChange={(value) => onMinimumLevelChange(value as OperationsMinimumLevel)}
					data={[{ value: 'warn', label: '경고 + 오류' }, { value: 'error', label: '오류만' }]}
				/>
				<SecondaryButton loading={loading} onClick={onReload}>새로고침</SecondaryButton>
			</Group>
			{error ? <Alert color="red" title="로그 조회 실패">{error}</Alert> : null}
			<SimpleGrid cols={{ base: 1, sm: 3 }} spacing="lg">
				<Metric label="경고" value={snapshot.warnings} detail="복구했지만 예상과 달랐던 상황" color="yellow" />
				<Metric label="오류" value={snapshot.errors} detail="해당 시나리오를 중단한 상황" color="red" />
				<Metric label="조회 건수" value={snapshot.matching} detail={snapshot.truncated ? '최근 로그 범위에서 집계 · 일부 생략' : '현재 로그 파일에서 집계'} color="blue" />
			</SimpleGrid>
			<Section title="반복되는 예상 불일치" description="빈도가 높은 이벤트부터 개선하면 됩니다." subtle>
				{snapshot.aggregates.length === 0 ? <EmptyState>현재 필터에 해당하는 로그가 없습니다.</EmptyState> : (
					<Stack gap="xs">
						{snapshot.aggregates.slice(0, 12).map((item) => (
							<Group key={`${item.level}:${item.scenario}:${item.operation}:${item.event}`} justify="space-between" align="flex-start" bg="dark.6" p="md" wrap="nowrap">
								<Stack gap={3} style={{ minWidth: 0 }}>
									<Group gap="xs"><Badge color={item.level === 'ERROR' ? 'red' : 'yellow'}>{item.level}</Badge><Text size="sm" fw={700}>{scenarioLabel(item.scenario)}</Text></Group>
									<Text size="sm">{item.event}</Text>
									<Text size="xs" c="dimmed">{item.operation || 'operation 없음'} · 최근 {formatLogTime(item.last_time)}</Text>
									{item.last_error ? <Text size="xs" c="red.3">{item.last_error}</Text> : null}
								</Stack>
								<Badge size="lg" variant="light" color="gray">{item.count}회</Badge>
							</Group>
						))}
					</Stack>
				)}
			</Section>
			<Section title="최근 경고·오류" description="5초마다 자동 갱신합니다." subtle>
				{snapshot.entries.length === 0 ? <EmptyState>현재 필터에 해당하는 로그가 없습니다.</EmptyState> : (
					<Stack gap="sm">
						{snapshot.entries.map((entry) => {
							const extra = details(entry.fields);
							return (
								<Box key={`${entry.sequence}:${entry.time}:${entry.event}`} bg="dark.6" p="md">
									<Stack gap="xs">
										<Group justify="space-between" align="flex-start" wrap="wrap">
											<Group gap="xs"><Badge color={entry.level === 'ERROR' ? 'red' : 'yellow'}>{entry.level}</Badge><Text fw={700}>{scenarioLabel(entry.scenario)}</Text></Group>
											<Text size="xs" c="dimmed">{formatLogTime(entry.time)}</Text>
										</Group>
										<Text size="sm">{entry.event}</Text>
										{entry.operation ? <Text size="xs" c="dimmed">작업: {entry.operation}</Text> : null}
										{entry.expected || entry.observed ? <Text size="xs">예상: {entry.expected || '-'} · 실제: {entry.observed || '-'}</Text> : null}
										{entry.error ? <Text size="sm" c="red.3">{entry.error}</Text> : null}
										{extra ? <Code block>{extra}</Code> : null}
									</Stack>
								</Box>
							);
						})}
					</Stack>
				)}
			</Section>
		</Stack>
	);
}
