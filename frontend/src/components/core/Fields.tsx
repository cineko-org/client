import {
  Checkbox, NumberInput, PasswordInput, Select, Textarea, TextInput,
  type CheckboxProps, type NumberInputProps, type PasswordInputProps, type SelectProps, type TextareaProps, type TextInputProps,
} from '@mantine/core';

export function TextField(props: TextInputProps) {
  return <TextInput radius={0} {...props} />;
}

export function TextAreaField(props: TextareaProps) {
  return <Textarea radius={0} autosize {...props} />;
}

export function PasswordField(props: PasswordInputProps) {
  return <PasswordInput radius={0} {...props} />;
}

export function SelectField(props: SelectProps) {
  return <Select radius={0} searchable {...props} />;
}

export function NumberField(props: NumberInputProps) {
  return <NumberInput radius={0} {...props} />;
}

export function CheckField(props: CheckboxProps) {
  return <Checkbox radius={0} {...props} />;
}
