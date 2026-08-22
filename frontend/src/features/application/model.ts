import { create } from '@bufbuild/protobuf';
import { CatalogIndexSchema, WebUIStateSchema, type WebUIState } from '../../api/proto';
export { initialApplicationConnection, type ApplicationConnection } from '../../shared/application';

export const emptyAppState: WebUIState = create(WebUIStateSchema, {
	userId: 'local-user',
	catalog: create(CatalogIndexSchema),
});
