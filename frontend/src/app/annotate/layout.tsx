import type { Metadata } from 'next';

export const metadata: Metadata = {
  title: '标注任务',
  robots: {
    index: false,
    follow: false,
    nocache: true,
  },
  referrer: 'no-referrer',
};

export default function AnnotationLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return children;
}
