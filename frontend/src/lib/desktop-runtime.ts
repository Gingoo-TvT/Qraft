export type DesktopConfig = { schema_version: number; mode: 'remote' | 'local'; server_url: string; local_port: number };
export type ThemeColors = {
 accent: string; background: string; surface: string; sidebar: string; text: string; muted: string; border: string;
};
export type DesktopTheme = {
 schema: 'algoforge.desktop-theme'; version: 1; id: string; name: string;
 colors: { light: ThemeColors; dark: ThemeColors };
};
export type DesktopPreferences = {
 theme: 'system' | 'light' | 'dark'; density: 'comfortable' | 'compact'; sidebar_collapsed: boolean; last_path: string;
 color_theme?: string; custom_themes?: DesktopTheme[];
};
export type DesktopState = {
 version: string; data_dir: string; packaged: boolean; configured: boolean; service_url: string; config: DesktopConfig;
 update: { status: string; current_version: string; latest_version?: string; release_url?: string; message?: string; error?: string; bytes: number; total: number };
 operation: { busy: boolean; action: string; message: string; error: string; updated: string };
};
export type DesktopBootstrap = {
 base: string; api_base: string; ui_base: string; native: boolean;
 state: DesktopState; preferences: DesktopPreferences;
};
declare global { interface Window { __ALGOFORGE_DESKTOP__?: DesktopBootstrap } }
export function desktopRuntime(): DesktopBootstrap | undefined {
 return typeof window === 'undefined' ? undefined : window.__ALGOFORGE_DESKTOP__;
}
