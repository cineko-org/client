import { fromJson, toJson, type JsonValue, type Message } from '@bufbuild/protobuf';
import type { GenMessage } from '@bufbuild/protobuf/codegenv2';

export interface DesktopBridge {
	GetNetworkSettings(): Promise<string>;
	SaveNetworkSettings(input: string): Promise<string>;
	GetHookSettings(): Promise<string>;
	SaveHookSettings(input: string): Promise<string>;
	GetUserID(): Promise<string>;
	RecordClientLog(payload: string): Promise<void>;
	WatchSeatMap(auditoriumId: string): Promise<void>;
	StopSeatMapWatch(): Promise<void>;
	Exit(): Promise<void>;
}

export function decodeDesktopProto<T extends Message>(schema: GenMessage<T>, payload: string): T {
	return fromJson(schema, JSON.parse(payload) as JsonValue, { ignoreUnknownFields: false });
}

export function encodeDesktopProto<T extends Message>(schema: GenMessage<T>, message: T): string {
	return JSON.stringify(toJson(schema, message));
}

declare global {
	interface Window {
		go?: { main?: { DesktopApp?: DesktopBridge } };
		runtime?: {
			EventsOn?: (name: string, callback: (...args: unknown[]) => void) => (() => void);
			LogInfo?: (message: string) => void;
			LogWarning?: (message: string) => void;
			LogError?: (message: string) => void;
		};
		__cinekoAppBooted?: boolean;
	}
}
