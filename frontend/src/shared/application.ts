export interface ApplicationConnection {
	status: 'loading' | 'ready' | 'stale' | 'unavailable';
	message: string;
	lastSuccessfulAt: string;
	retrying: boolean;
}

export const initialApplicationConnection: ApplicationConnection = {
	status: 'loading',
	message: '',
	lastSuccessfulAt: '',
	retrying: false,
};
