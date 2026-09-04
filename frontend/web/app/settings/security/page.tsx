import { Shell } from "@/components/shell";
import { MfaSettings } from "@/components/mfa";

export const metadata = { title: "Security" };

export default function SecurityPage() {
  return (
    <Shell>
      <div className="mx-auto max-w-xl space-y-4">
        <h1 className="text-xl font-semibold">Security</h1>
        <MfaSettings />
        <p className="text-xs text-zinc-500">
          Sign-in links and passwords are managed by your hQube account. If you lose your authenticator, contact hQube to reset it.
        </p>
      </div>
    </Shell>
  );
}
