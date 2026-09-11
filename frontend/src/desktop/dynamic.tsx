import React, { ComponentType, lazy, Suspense } from 'react';
type Result<P> = ComponentType<P> | { default: ComponentType<P> };
export default function dynamic<P extends object = Record<string, never>>(
 loader: () => Promise<Result<P>>,
 options?: { ssr?: boolean; loading?: ComponentType },
): ComponentType<P> {
 const Component = lazy(async () => {
  const loaded = await loader();
  return typeof loaded === 'function' ? { default: loaded } : loaded as { default: ComponentType<P> };
 }) as unknown as ComponentType<P>;
 const Loading = options?.loading;
 return function DynamicComponent(props: P) {
  return <Suspense fallback={Loading ? <Loading /> : null}><Component {...props} /></Suspense>;
 };
}
