import { Chip, Group, SegmentedControl, Stack, Text } from '@mantine/core';
import { DatePickerInput, TimePicker } from '@mantine/dates';
import { Columns } from '../../../components/core/Columns';
import { NumberField } from '../../../components/core/Fields';
import {
  normalizeHorizon, scheduleBounds, scheduleDescription, weekdayOptions, type MonitorForm,
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
      <Stack gap="xs">
        <Group justify="space-between" align="flex-end" wrap="wrap">
          <Text fw={600}>예매 전략</Text>
          <SegmentedControl
            radius={0}
            aria-label="예매 전략"
            data={[{ label: '예매 오픈 대기', value: 'opening' }, { label: '취소표 대기', value: 'cancellation' }]}
            value={form.monitorMode}
            onChange={(value) => set({
              monitorMode: value as MonitorForm['monitorMode'],
              weekdays: value === 'cancellation' ? [] : form.weekdays,
            })}
          />
        </Group>
        <Text size="sm" c="dimmed">
          {form.monitorMode === 'opening'
            ? '상영 일정이 열리면 좌석 선택을 시작합니다.'
            : '열린 회차에서 선호 좌석이 풀릴 때까지 확인합니다.'}
        </Text>
      </Stack>

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
        {form.monitorMode === 'opening' ? (
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
              max={14}
              step={1}
              suffix="일"
              allowDecimal={false}
              allowNegative={false}
            />
          </Columns>
        ) : null}
        <Text size="sm" c="dimmed">{scheduleDescription(form)}</Text>
      </Stack>

      <Stack gap="sm">
        <Stack gap={2}>
          <Text fw={600}>선호 시간대</Text>
          <Text size="xs" c="dimmed">
            실제 상영 시작 시각 기준입니다. 시작은 포함하고 마감은 제외합니다. 비워두면 모든 회차를 확인합니다.
            시작이 마감보다 늦으면 자정을 넘어 확인합니다. 예: 21:00–06:00은 21:00 이상 또는 06:00 미만입니다.
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
