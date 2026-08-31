import { useState } from 'react';
import { Alert, Anchor, Badge, Box, Code, Group, Modal, SegmentedControl, SimpleGrid, Stack, Text } from '@mantine/core';
import { DangerButton, PrimaryButton, SecondaryButton } from '../../../components/core/Actions';
import { Metric } from '../../../components/core/Metric';
import { EmptyState, Section } from '../../../components/core/Section';
import {
	formatLogTime, scenarioLabel,
	type NetworkCaptureSnapshot,
	type OperationsLogSnapshot, type OperationsMinimumLevel,
} from '../model';

export interface OperationsLogViewProps {
	minimumLevel: OperationsMinimumLevel;
	snapshot: OperationsLogSnapshot;
	loading: boolean;
	error: string;
	network?: NetworkCaptureSnapshot;
	selectedNetwork?: Record<string, unknown> | null;
	selectedNetworkID?: string;
	clearing?: boolean;
	onMinimumLevelChange: (value: OperationsMinimumLevel) => void;
	onReload: () => void;
	onInspectNetwork?: (id: string) => void;
	onClear: () => Promise<void>;
}

function details(fields: Record<string, unknown> | undefined): string {
	if (!fields || Object.keys(fields).length === 0) return '';
	return JSON.stringify(fields, null, 2);
}

export function OperationsLogView({
	minimumLevel, snapshot, loading, error, network, selectedNetwork, selectedNetworkID, clearing,
	onMinimumLevelChange, onReload, onInspectNetwork, onClear,
}: OperationsLogViewProps) {
	const [confirmingClear, setConfirmingClear] = useState(false);
	const clear = async () => {
		try {
			await onClear();
			setConfirmingClear(false);
		} catch {
			// The hook exposes the actionable error in the operations page.
		}
	};
	return (
		<Stack gap="xl">
			<Group justify="space-between" align="flex-end" wrap="wrap">
				<SegmentedControl
					value={minimumLevel}
					onChange={(value) => onMinimumLevelChange(value as OperationsMinimumLevel)}
					data={[{ value: 'warn', label: '경고 + 오류' }, { value: 'error', label: '오류만' }]}
				/>
				<Group gap="xs">
					<DangerButton onClick={() => setConfirmingClear(true)}>로그 비우기</DangerButton>
					<SecondaryButton loading={loading} onClick={onReload}>새로고침</SecondaryButton>
				</Group>
			</Group>
			{error ? <Alert color="red" title="로그 조회 실패">{error}</Alert> : null}
			<SimpleGrid cols={{ base: 1, sm: 2, lg: 4 }} spacing="lg">
				<Metric label="경고" value={snapshot.warnings} detail="복구했지만 예상과 달랐던 상황" color="yellow" />
				<Metric label="오류" value={snapshot.errors} detail="해당 시나리오를 중단한 상황" color="red" />
				<Metric label="CGV 요청 전송" value={network?.statistics.provider_sent ?? 0} detail="실제로 cgv.co.kr로 전송한 요청" color="blue" />
				<Metric label="HTTP 429" value={network?.statistics.status_429 ?? 0} detail="CGV가 실제로 반환한 요청 제한 응답" color="orange" />
			</SimpleGrid>
			<Text size="xs" c="dimmed">
				로컬에서 전송하지 않은 브라우저 리소스 {network?.statistics.blocked ?? 0}건 · HTTP 응답이나 오류가 아닙니다.
			</Text>
			<Section title="반복되는 예상 불일치" description="이번 실행에서 빈도가 높은 이벤트부터 표시합니다." subtle>
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
			<Section title="최근 네트워크 오류" description="요청·응답 헤더와 본문을 원본 artifact로 확인합니다." subtle>
				{!network || network.entries.length === 0 ? <EmptyState>최근 HTTP 오류 artifact가 없습니다.</EmptyState> : (
					<Stack gap="xs">
						{network.entries.map((entry) => (
							<Box key={entry.id} bg="dark.6" p="md">
								<Stack gap="xs">
									<Group justify="space-between" align="flex-start" wrap="wrap">
										<Group gap="xs">
											<Badge color={entry.status === 429 ? 'orange' : 'red'}>HTTP {entry.status || '실패'}</Badge>
											<Text size="sm" fw={700}>{entry.method} · {entry.service}</Text>
										</Group>
										<Text size="xs" c="dimmed">{formatLogTime(entry.completed_at)}</Text>
									</Group>
									<Text size="xs" style={{ overflowWrap: 'anywhere' }}>{entry.url}</Text>
									{entry.error ? <Text size="xs" c="red.3">{entry.error}</Text> : null}
									<Group gap="sm">
										{onInspectNetwork ? <SecondaryButton size="xs" loading={selectedNetworkID === entry.id} onClick={() => onInspectNetwork(entry.id)}>요청·응답 보기</SecondaryButton> : null}
										{entry.request_body_sha256 ? <Anchor size="xs" href={`/api/logs/network/${encodeURIComponent(entry.id)}/body/request`} target="_blank">요청 본문</Anchor> : null}
										{entry.response_body_sha256 ? <Anchor size="xs" href={`/api/logs/network/${encodeURIComponent(entry.id)}/body/response`} target="_blank">응답 본문</Anchor> : null}
									</Group>
								</Stack>
							</Box>
						))}
						{selectedNetwork ? <Code block>{JSON.stringify(selectedNetwork, null, 2)}</Code> : null}
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
			<Modal opened={confirmingClear} onClose={() => setConfirmingClear(false)} title="로컬 로그 비우기">
				<Stack gap="lg">
					<Text size="sm">구조화 로그와 저장된 네트워크 요청·응답 원본을 모두 삭제할까요? 삭제한 진단 자료는 복구할 수 없습니다.</Text>
					<Group justify="flex-end">
						<SecondaryButton disabled={clearing} onClick={() => setConfirmingClear(false)}>취소</SecondaryButton>
						<PrimaryButton color="red" loading={clearing} onClick={() => void clear()}>모두 비우기</PrimaryButton>
					</Group>
				</Stack>
			</Modal>
		</Stack>
	);
}
