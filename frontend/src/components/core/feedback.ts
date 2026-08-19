export type FeedbackTone = 'info' | 'success' | 'warning' | 'error';

export interface NotifyOptions {
	tone?: FeedbackTone;
	important?: boolean;
}

export type Notify = (message: string, options?: NotifyOptions) => void;
