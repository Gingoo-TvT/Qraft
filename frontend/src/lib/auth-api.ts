import { getApiBaseUrl } from './api';
import { serviceFetch, type AuthSession, type AccountUser } from './auth-session';
export type AccountInvitation = { id: string; email: string; kind: 'register' | 'reset'; expires_at: string; used_at?: string; revoked_at?: string };
export async function authRequest<T>(path: string, method = 'GET', body?: unknown, invitation?: string): Promise<T> {
 const response = await serviceFetch(getApiBaseUrl() + '/api/v1' + path, {
  method, headers: { 'Content-Type': 'application/json', ...(invitation ? { 'X-Qraft-Invitation-Token': invitation } : {}) },
  ...(body === undefined ? {} : { body: JSON.stringify(body) }),
 }, invitation !== undefined, path.startsWith('/auth/'));
 const payload = await response.json().catch(() => null);
 if (!response.ok || payload?.success === false) {
  const error = new Error(payload?.error?.message || '请求失败（' + response.status + '）');
  Object.assign(error, { status: response.status }); throw error;
 }
 return (payload?.data ?? payload) as T;
}
export const getSession = () => authRequest<AuthSession>('/auth/session');
export const login = (email: string, password: string) => authRequest<AuthSession>('/auth/login', 'POST', { email, password });
export const logout = () => authRequest<void>('/auth/logout', 'POST');
export const changePassword = (current_password: string, new_password: string) => authRequest<void>('/auth/password', 'POST', { current_password, new_password });
export const bootstrap = (token: string, email: string, password: string, display_name: string) => authRequest<AuthSession>('/auth/bootstrap', 'POST', { token, email, password, display_name });
export const getInvitation = (token: string) => authRequest<AccountInvitation>('/auth/invitation', 'GET', undefined, token);
export const redeemInvitation = (kind: 'register' | 'reset', token: string, password: string, display_name: string) => authRequest<AuthSession | null>(kind === 'register' ? '/auth/register' : '/auth/reset-password', 'POST', { token, password, ...(kind === 'register' ? { display_name } : {}) });
export const listUsers = () => authRequest<{ items: AccountUser[] }>('/admin/users');
export const updateUser = (id: string, value: { role?: 'admin' | 'member'; disabled?: boolean }) => authRequest<AccountUser>('/admin/users/' + encodeURIComponent(id), 'PATCH', value);
export const listInvitations = () => authRequest<{ items: AccountInvitation[] }>('/admin/invitations');
export const issueAccountInvitation = (email: string, kind: 'register' | 'reset') => authRequest<AccountInvitation & { token: string }>('/admin/invitations', 'POST', { email, kind });
export const revokeAccountInvitation = (id: string) => authRequest<void>('/admin/invitations/' + encodeURIComponent(id), 'DELETE');
