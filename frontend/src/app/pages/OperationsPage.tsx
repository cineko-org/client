import { Stack } from '@mantine/core';
import { PageHeader } from '../../components/core/PageHeader';
import { OperationsLogView } from '../../features/operations/ui/OperationsLogView';
import { useOperationsLogs } from '../../features/operations/useOperationsLogs';

export function OperationsPage() {
	const logs = useOperationsLogs();
	return (
		<Stack gap="xl">
			<PageHeader title="관제" description="포스터·카탈로그·일정·모니터링·좌석 선택에서 예상과 달랐던 상황을 모아봅니다." />
			<OperationsLogView
				minimumLevel={logs.minimumLevel}
				snapshot={logs.snapshot}
				loading={logs.loading}
				error={logs.error}
				network={logs.network}
				selectedNetwork={logs.selectedNetwork}
				selectedNetworkID={logs.selectedNetworkID}
				clearing={logs.clearing}
				onMinimumLevelChange={logs.setMinimumLevel}
				onReload={() => void logs.reload()}
				onInspectNetwork={(id) => void logs.inspectNetwork(id)}
				onClear={logs.clearLogs}
			/>
		</Stack>
	);
}
