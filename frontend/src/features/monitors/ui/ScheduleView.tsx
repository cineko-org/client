import { Chip, Group, Stack, Text } from '@mantine/core';
import { DatePickerInput, TimePicker } from '@mantine/dates';
import { Columns } from '../../../components/core/Columns';
import { NumberField } from '../../../components/core/Fields';
import {
  maximumSearchHorizonDays, normalizeHorizon, scheduleBounds, scheduleDescription, weekdayOptions, type MonitorForm,
} from '../model';

export interface ScheduleViewProps {
  form: MonitorForm;
  onChange: (form: MonitorForm) => void;
}

export function ScheduleView({ form, onChange }: ScheduleViewProps) {
  const bounds = scheduleBounds(new Date());
  const set = (patch: Partial<MonitorForm>) => onChange({ ...form, ...patch });

  return (
    <Stack gap="lg">
      <Text size="sm" c="dimmed">예매 오픈부터 취소표까지 계속 확인합니다.</Text>

      <Stack gap="sm">
        <Stack gap={2}>
          <Text fw={600}>관람 일정</Text>
          <Text size="xs" c="dimmed">날짜와 반복 요일을 함께 추가할 수 있습니다.</Text>
        </Stack>
        <DatePickerInput
          radius={0}
          type="multiple"
          label="날짜"
          placeholder="미래 날짜 추가"
          value={form.dates}
          onChange={(dates) => set({ dates })}
          minDate={bounds.today}
          maxDate={bounds.last}
          valueFormat="YYYY년 M월 D일"
          clearable
        />
        <Columns>
          <Stack gap="xs">
            <Text size="sm" fw={500}>반복 요일</Text>
            <Chip.Group multiple value={form.weekdays} onChange={(weekdays) => set({ weekdays })}>
              <Group gap={6} wrap="wrap">
                {weekdayOptions.map((weekday) => <Chip key={weekday.value} value={weekday.value}>{weekday.label}</Chip>)}
              </Group>
            </Chip.Group>
          </Stack>
          <NumberField
            label="한 번에 확인할 기간"
            value={form.horizonDays}
            onChange={(value) => set({ horizonDays: normalizeHorizon(value) })}
            min={1}
            max={maximumSearchHorizonDays}
            step={1}
            suffix="일"
            allowDecimal={false}
            allowNegative={false}
          />
        </Columns>
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
