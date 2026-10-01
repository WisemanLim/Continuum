import { Component, type ReactNode } from "react";
import { useT } from "./i18n";

function ErrorContent({ msg, onRetry }: { msg: string; onRetry: () => void }) {
  const t = useT();
  return (
    <div className="banner danger" role="alert">
      <span className="spacer">{t("common.screenError", { n: msg })}</span>
      <button className="btn sm" onClick={onRetry}>
        {t("common.retry")}
      </button>
    </div>
  );
}

/** Keeps the shell alive when one page throws (CTM-COM-040 error state). */
export class ErrorBoundary extends Component<{ children: ReactNode; resetKey: string }, { error: Error | null }> {
  override state = { error: null as Error | null };
  static getDerivedStateFromError(error: Error) {
    return { error };
  }
  override componentDidUpdate(prev: { resetKey: string }) {
    if (prev.resetKey !== this.props.resetKey && this.state.error) this.setState({ error: null });
  }
  override render() {
    if (this.state.error)
      return <ErrorContent msg={this.state.error.message} onRetry={() => this.setState({ error: null })} />;
    return this.props.children;
  }
}
