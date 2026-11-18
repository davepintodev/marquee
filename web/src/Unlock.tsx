import { useId, useState } from "react";

interface Props {
  // True when a token was sent and marquee refused it.
  rejected: boolean;
  onSubmit: (token: string) => void;
}

export function Unlock({ rejected, onSubmit }: Props) {
  const fieldId = useId();
  const [token, setToken] = useState("");

  return (
    <div className="screen">
      <form
        className="unlock"
        onSubmit={(e) => {
          e.preventDefault();
          const t = token.trim();
          if (!t) return;
          setToken("");
          onSubmit(t);
        }}
      >
        <p>This marquee asks for its API token. You only need to enter it once on this device.</p>
        {rejected && (
          <p className="unlock-error" role="alert">
            That token was not accepted. Check it and try again.
          </p>
        )}
        <label className="sr" htmlFor={fieldId}>
          API token
        </label>
        <input
          id={fieldId}
          type="password"
          placeholder="api token"
          autoComplete="off"
          autoCapitalize="off"
          spellCheck={false}
          autoFocus
          value={token}
          onChange={(e) => setToken(e.target.value)}
        />
        <button type="submit" className="btn-primary">
          Unlock
        </button>
      </form>
    </div>
  );
}
