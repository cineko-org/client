export interface ScannerForm {
  url: string;
  token: string;
}

export interface ScannerState {
  phase: 'loading' | 'ready' | 'saving' | 'error';
  hasToken: boolean;
  message: string;
}

interface SavedScannerSettings {
  url: string;
  hasToken: boolean;
}

// Validate the desktop boundary instead of trusting a TypeScript assertion.
export function decodeScannerSettings(payload: string): SavedScannerSettings {
  const value: unknown = JSON.parse(payload);
  if (!value || typeof value !== 'object' ||
      !('url' in value) || typeof value.url !== 'string' ||
      !('hasToken' in value) || typeof value.hasToken !== 'boolean') {
    throw new Error('SOXY 설정 응답이 올바르지 않습니다.');
  }
  return { url: value.url, hasToken: value.hasToken };
}

export interface ScannerSettingsController {
  available: boolean;
  form: ScannerForm;
  state: ScannerState;
  setForm: (form: ScannerForm) => void;
  load: () => Promise<void>;
  save: () => Promise<void>;
}
