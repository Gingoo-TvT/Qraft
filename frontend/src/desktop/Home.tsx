import WorkspaceHome from '@/components/workspace/WorkspaceHome';
import { useDesktop } from './runtime';

export default function Home() {
 const { connection } = useDesktop();
 return <WorkspaceHome serviceReady={Boolean(connection?.ready)} desktop settingsHref="/desktop/settings" />;
}
