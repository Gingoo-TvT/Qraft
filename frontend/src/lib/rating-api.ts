import { APIError, getApiBaseUrl } from './api';
import { desktopRuntime } from './desktop-runtime';
import { reviewRequestInit } from '@/components/rating/review-session';
import type { Anchor, Assessment, Calibration, Decision, DecisionInput, FeedbackInput, Invitation, InvitationRequest, IssuedInvitation, ReviewTask, Workspace } from './rating-types';

export function ratingError(error: unknown): string {
 if (error instanceof APIError && error.status === 404) return '当前服务尚未提供题目评估接口，或该记录不存在。请确认服务已更新，再重新加载。';
 if (error instanceof APIError && (error.status === 401 || error.status === 403)) return '操作未获授权。管理操作需要工作区管理员凭据；评价令牌仅能用于受邀评价。';
 return error instanceof Error ? error.message : '请求失败，请稍后重试。';
}
export function reviewError(error: unknown): string {
 if (error instanceof APIError && (error.status === 401 || error.status === 403 || error.status === 410)) return '评价邀请已过期、撤销或无效。请联系邀请人重新发放，已保存的评价不会因此删除。';
 return ratingError(error);
}
async function request<T>(path: string, method = 'GET', payload?: unknown, publicToken?: string): Promise<T> {
 const desktop = desktopRuntime();
 if (desktop && !desktop.state.configured) throw new Error('请先在客户端中选择并保存服务连接，再打开评价。');
 let init: RequestInit;
 if (publicToken !== undefined) init = reviewRequestInit(publicToken, payload);
 else {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' };
  if (typeof window !== 'undefined') {
   let token: string | null = null;
   try { token = window.localStorage.getItem('algoforge_token'); } catch { /* Server will explain missing authorization. */ }
   if (token) headers.Authorization = 'Bearer ' + token;
  }
  init = { method, headers, cache: 'no-store', ...(payload === undefined ? {} : { body: JSON.stringify(payload) }) };
 }
 let response: Response;
 try { response = await fetch(getApiBaseUrl() + path, init); } catch { throw new Error('无法连接题目评估服务。请检查连接后重试。'); }
 const body = await response.json().catch(() => null);
 if (!response.ok || body?.success === false) throw new APIError(body?.error?.message ?? '请求失败（' + response.status + '）', body?.error?.code ?? 'RATING_ERROR', response.status);
 return body?.data as T;
}
const problemPath = (id: string) => '/api/v1/rating/problems/' + encodeURIComponent(id);
export const getRatingWorkspace = (id: string) => request<Workspace>(problemPath(id));
export const startAssessment = (id: string) => request<Assessment>(problemPath(id) + '/assessments', 'POST');
export const getAssessment = (id: string) => request<Assessment>('/api/v1/rating/assessments/' + encodeURIComponent(id));
export const cancelAssessment = (id: string) => request<Assessment>('/api/v1/rating/assessments/' + encodeURIComponent(id), 'DELETE');
export const listAnchors = () => request<Anchor[]>('/api/v1/rating/anchors');
export const createAnchor = (value: Partial<Anchor>) => request<Anchor>('/api/v1/rating/anchors', 'POST', value);
export const issueInvitation = (id: string, value: InvitationRequest) => request<IssuedInvitation>(problemPath(id) + '/invitations', 'POST', value);
export const listInvitations = (id: string) => request<Invitation[]>(problemPath(id) + '/invitations');
export const revokeInvitation = (id: string, invitation: string) => request<void>(problemPath(id) + '/invitations/' + encodeURIComponent(invitation), 'DELETE');
export const calibrateRating = (id: string) => request<Calibration>(problemPath(id) + '/calibration', 'POST');
export const decideRating = (id: string, value: DecisionInput) => request<Decision>(problemPath(id) + '/decisions', 'POST', value);
export const getReviewTask = (token: string) => request<ReviewTask>('/api/v1/public/rating/review', 'GET', undefined, token);
export const submitRatingFeedback = (token: string, value: FeedbackInput) => request<{ revision: number; updated_at: string }>('/api/v1/public/rating/review', 'POST', value, token);
