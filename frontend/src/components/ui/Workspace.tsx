import type { ReactNode } from 'react';
import { Inbox, type LucideIcon } from 'lucide-react';

type HeadingProps = {
 eyebrow?: ReactNode; title: ReactNode; description?: ReactNode; actions?: ReactNode; children?: ReactNode;
};
export function PageHeader({ eyebrow, title, description, actions, children }: HeadingProps) {
 return <header className="af-page-header"><div className="af-page-header-row"><div className="af-heading-copy">
  {eyebrow && <div className="af-eyebrow">{eyebrow}</div>}<h1>{title}</h1>
  {description && <div className="af-description">{description}</div>}
 </div>{actions && <div className="af-heading-actions">{actions}</div>}</div>{children && <div className="af-header-detail">{children}</div>}</header>;
}
export function SectionHeading({ eyebrow, title, description, actions, children }: HeadingProps) {
 return <header className="af-section-heading"><div className="af-section-heading-row"><div className="af-heading-copy">
  {eyebrow && <div className="af-eyebrow">{eyebrow}</div>}<h2>{title}</h2>
  {description && <div className="af-description">{description}</div>}
 </div>{actions && <div className="af-heading-actions">{actions}</div>}</div>{children}</header>;
}
export function EmptyState({ icon: Icon = Inbox, title, description, action }: {
 icon?: LucideIcon; title: ReactNode; description?: ReactNode; action?: ReactNode;
}) {
 return <div className="af-empty"><span className="af-empty-icon"><Icon size={25} strokeWidth={1.5} /></span><h3>{title}</h3>
  {description && <div className="af-empty-description">{description}</div>}{action && <div className="af-empty-action">{action}</div>}</div>;
}
export function FormSection({ number, title, description, children, id }: {
 number?: string; title: ReactNode; description?: ReactNode; children: ReactNode; id?: string;
}) {
 return <section id={id} className="af-form-section" aria-labelledby={id ? id + '-title' : undefined}>
  <header className="af-form-section-heading">{number && <span className="af-form-number">{number}</span>}<div><h2 id={id ? id + '-title' : undefined}>{title}</h2>
  {description && <div className="af-description">{description}</div>}</div></header><div className="af-form-section-body">{children}</div>
 </section>;
}
