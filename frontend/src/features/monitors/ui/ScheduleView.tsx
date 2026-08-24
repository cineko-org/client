import { Chip, Group, Stack, Text } from '@mantine/core';
import { TimePicker } from '@mantine/dates';
import { Columns } from '../../../components/core/Columns';
import { scheduleDescription, weekdayOptions, type MonitorForm } from '../model';

export interface ScheduleViewProps {
  form: MonitorForm;
  onChange: (form: MonitorForm) => void;
}

export function ScheduleView({ form, onChange }: ScheduleViewProps) {
  const set = (patch: Partial<MonitorForm>) => onChange({ ...form, ...patch });

  return (
    <Stack gap="lg">
      <Text size="sm" c="dimmed">예매 오픈부터 취소표까지 계속 확인합니다.</Text>

      <Stack gap="sm">
        <Stack gap={2}>
          <Text fw={600}>관람 일정</Text>
          <Text size="xs" c="dimmed">선택한 요일의 신규 일정과 취소표를 기한 없이 확인합니다.</Text>
        </Stack>
        <Stack gap="xs">
          <Text size="sm" fw={500}>반복 요일</Text>
          <Chip.Group multiple value={form.weekdays} onChange={(weekdays) => set({ weekdays })}>
            <Group gap={6} wrap="wrap">
              {weekdayOptions.map((weekday) => <Chip key={weekday.value} value={weekday.value}>{weekday.label}</Chip>)}
            </Group>
          </Chip.Group>
        </Stack>
        <Text size="sm" c="dimmed">{scheduleDescription(form)}</Text>
      </Stack>

      <Stack gap="sm">
        <Stack gap={2}>
          <Text fw={600}>선호 시간대</Text>
          <Text size="xs" c="dimmed">
            비우면 모든 시간을 확인합니다. 21:00–06:00처럼 자정을 넘기는 시간대도 설정할 수 있습니다.
          </Text>
        </Stack>
        <Columns>
          <TimePicker radius={0} label="시작" value={form.earliestTime} onChange={(earliestTime) => set({ earliestTime })} minutesStep={5} withDropdown clearable />
          <TimePicker radius={0} label="마감" value={form.latestTime} onChange={(latestTime) => set({ latestTime })} minutesStep={5} withDropdown clearable />
        </Columns>
      </Stack>
    </Stack>
  );
}
