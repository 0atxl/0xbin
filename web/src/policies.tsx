import { useEffect, useRef, type MouseEvent } from "react";
import { hostedPagePaths, type HostedPage } from "./hosted";

const contactEmail = "hello@atulk.me";
const contactHref = `mailto:${contactEmail}`;
const githubURL = "https://github.com/0atxl/0xbin";
const cloudflarePrivacyURL = "https://www.cloudflare.com/privacypolicy/";
const lastUpdated = "8 August 2026";

type Navigate = (path: string) => void;

const menuItems: Array<{ page: HostedPage; label: string }> = [
  { page: "about", label: "Who & why" },
  { page: "terms", label: "Terms & conditions" },
  { page: "privacy", label: "Privacy & reports" },
];

export function HostedMenu({
  currentPage,
  onNavigate,
}: {
  currentPage?: HostedPage;
  onNavigate: Navigate;
}) {
  const detailsRef = useRef<HTMLDetailsElement>(null);

  useEffect(() => {
    function closeOnEscape(event: KeyboardEvent) {
      if (event.key !== "Escape" || !detailsRef.current?.open) return;
      detailsRef.current.open = false;
      detailsRef.current.querySelector("summary")?.focus();
    }

    function closeOutside(event: PointerEvent) {
      if (
        detailsRef.current?.open &&
        event.target instanceof Node &&
        !detailsRef.current.contains(event.target)
      ) {
        detailsRef.current.open = false;
      }
    }

    document.addEventListener("keydown", closeOnEscape);
    document.addEventListener("pointerdown", closeOutside);
    return () => {
      document.removeEventListener("keydown", closeOnEscape);
      document.removeEventListener("pointerdown", closeOutside);
    };
  }, []);

  function follow(event: MouseEvent<HTMLAnchorElement>, path: string) {
    if (
      event.button !== 0 ||
      event.metaKey ||
      event.ctrlKey ||
      event.shiftKey ||
      event.altKey
    ) {
      return;
    }
    event.preventDefault();
    if (detailsRef.current) detailsRef.current.open = false;
    onNavigate(path);
  }

  return (
    <details className="site-menu" ref={detailsRef}>
      <summary aria-label="Site menu" title="About and policies">
        <MenuIcon />
      </summary>
      <nav aria-label="About and policies">
        {menuItems.map(({ page, label }) => (
          <a
            href={hostedPagePaths[page]}
            aria-current={currentPage === page ? "page" : undefined}
            onClick={(event) => follow(event, hostedPagePaths[page])}
            key={page}
          >
            {label}
          </a>
        ))}
        <span className="site-menu-separator" aria-hidden="true" />
        <a href={githubURL}>GitHub</a>
      </nav>
    </details>
  );
}

export function PolicyPage({ page }: { page: HostedPage }) {
  return (
    <main className="policy-page">
      <article>{policyContent(page)}</article>
    </main>
  );
}

function policyContent(page: HostedPage) {
  switch (page) {
    case "about":
      return <AboutPage />;
    case "terms":
      return <TermsPage />;
    case "privacy":
      return <PrivacyPage />;
  }
}

function PageHeader({
  title,
  summary,
  dated = false,
}: {
  title: string;
  summary: string;
  dated?: boolean;
}) {
  return (
    <header className="policy-heading">
      <p className="policy-kicker">0xbin</p>
      <h1>{title}</h1>
      <p>{summary}</p>
      {dated ? <p className="policy-date">Last updated {lastUpdated}</p> : null}
    </header>
  );
}

function AboutPage() {
  return (
    <>
      <PageHeader
        title="Who & why"
        summary="An independent open-source project for quick, understandable temporary sharing."
      />
      <section>
        <h2>Temporary by design</h2>
        <p>
          0xbin shares text, code, logs, and configuration without accounts or
          permanent publishing. Every paste expires after one hour, one day, or
          three days. A View-once paste is destroyed after one deliberate reveal
          and expires unopened after three days.
        </p>
      </section>
      <section>
        <h2>Memorable links</h2>
        <p>
          Paste addresses use three ordinary words instead of long random
          identifiers. Those links are unlisted, not secret. Anyone who obtains
          an unencrypted link may be able to read it until it expires.
        </p>
      </section>
      <section>
        <h2>Encryption when it matters</h2>
        <p>
          Optional encryption happens in your browser with AES-GCM. The key
          stays after the <code>#</code> in the sharing URL, which browsers do
          not send to the server. The server stores ciphertext but never
          receives the key.
        </p>
      </section>
      <section>
        <h2>Open and self-hostable</h2>
        <p>
          0xbin is open-source software built as one Go service with an embedded
          web interface and SQLite storage. You can inspect the implementation
          or run your own instance from the{" "}
          <a href={githubURL}>public GitHub repository</a>.
        </p>
      </section>
      <section>
        <h2>Who made this</h2>
        <p>
          0xbin is an independent open-source project created and maintained by
          Atul. It was made to work equally well as a public utility and a
          self-hosted tool, with its product decisions, implementation, tests,
          and deployment files developed in public.
        </p>
      </section>
      <section>
        <h2>Contact and source</h2>
        <p>
          General questions can be sent to{" "}
          <a href={contactHref}>{contactEmail}</a>. Source code, issue tracking,
          and contribution guidance are available on{" "}
          <a href={githubURL}>GitHub</a>.
        </p>
      </section>
    </>
  );
}

function TermsPage() {
  return (
    <>
      <PageHeader
        title="Terms & conditions"
        summary="The terms, acceptable-use rules, and security guidance for the hosted service at 0xbin.app."
        dated
      />
      <section>
        <h2>Using the service</h2>
        <p>
          By using 0xbin.app, you agree to these terms and acceptable-use rules.
          If you do not agree, do not use the hosted service. You are
          responsible for the content you submit and must have the right to
          share it.
        </p>
      </section>
      <section>
        <h2>Acceptable use</h2>
        <p>You must not use 0xbin to:</p>
        <ul>
          <li>publish or facilitate unlawful content or activity;</li>
          <li>
            distribute malware, phishing material, stolen credentials, or
            instructions primarily intended to compromise systems;
          </li>
          <li>
            exploit, endanger, harass, threaten, impersonate, or expose private
            information about another person;
          </li>
          <li>
            publish sexual abuse or exploitation material, especially material
            involving minors;
          </li>
          <li>
            infringe copyright, trademark, privacy, confidentiality, or other
            rights;
          </li>
          <li>
            scan or enumerate paste addresses, bypass rate limits, disrupt the
            service, or impose excessive automated load; or
          </li>
          <li>
            misrepresent an unencrypted paste as private or use the service as
            durable storage.
          </li>
        </ul>
      </section>
      <section>
        <h2>Enforcement</h2>
        <p>
          Access may be limited and reported content may be removed when
          reasonably necessary to enforce these rules, protect users or systems,
          or comply with applicable law. Serious or repeated abuse may be
          reported to relevant service providers or authorities when required.
        </p>
      </section>
      <section>
        <h2>Temporary storage</h2>
        <p>
          0xbin is not a backup or permanent publishing service. Pastes expire,
          View-once pastes can disappear after one deliberate reveal, and
          content may be removed earlier for operational, security, abuse, or
          legal reasons. Keep your own copy of anything you need to retain.
        </p>
      </section>
      <section>
        <h2>Your content</h2>
        <p>
          You retain any rights you hold in submitted content. You grant the
          service limited permission to store, process, and transmit that
          content only as needed to provide, secure, and operate the service
          until it expires or is removed.
        </p>
      </section>
      <section>
        <h2>Security and confidentiality</h2>
        <p>
          An unencrypted paste is unlisted, not private. Three-word links are
          not access control. Optional client-side encryption protects content
          only when the key is kept confidential and the receiving browser
          obtains trustworthy 0xbin code. A compromised frontend could still
          capture plaintext or keys.
        </p>
        <p>
          LiveBin rooms are server-readable plaintext collaboration rooms. Their
          optional shared password controls entry but does not encrypt room
          content from the service. Participant presence is temporary and used
          only while the room is active.
        </p>
      </section>
      <section>
        <h2>Availability and liability</h2>
        <p>
          The service is provided on an “as available” basis without a promise
          of uninterrupted operation, preservation, fitness for a particular
          purpose, or recovery of content. To the extent permitted by applicable
          law, the operator is not liable for indirect, incidental, or
          consequential loss arising from use or inability to use the service.
          Rights that cannot legally be excluded remain unaffected.
        </p>
      </section>
      <section>
        <h2>Report a vulnerability</h2>
        <p>
          Send security reports to <a href={contactHref}>{contactEmail}</a>.
          Include affected components, reproduction steps, and impact. Do not
          include active paste contents, encryption keys, credentials, or
          personal data unless necessary and explicitly requested.
        </p>
        <p>
          The repository’s current disclosure guidance is available in{" "}
          <a href={`${githubURL}/blob/main/SECURITY.md`}>SECURITY.md</a>.
        </p>
      </section>
      <section>
        <h2>Changes and contact</h2>
        <p>
          The service or these terms may change as security, legal, and
          operational needs evolve. Material revisions will update the date on
          this page. Questions can be sent to{" "}
          <a href={contactHref}>{contactEmail}</a>.
        </p>
      </section>
    </>
  );
}

function PrivacyPage() {
  return (
    <>
      <PageHeader
        title="Privacy & reports"
        summary="How the hosted service at 0xbin.app handles information and reports of misuse."
        dated
      />
      <section>
        <h2>Scope</h2>
        <p>
          This policy applies to the public service at 0xbin.app. Independent
          self-hosted installations are operated by their respective owners and
          have their own data practices.
        </p>
      </section>
      <section>
        <h2>Paste data</h2>
        <p>
          For an unencrypted paste, 0xbin stores its text and optional title and
          language until expiry. Unencrypted pastes are unlisted, not private.
          For an encrypted paste, your browser encrypts the content and metadata
          before upload; the server stores the resulting ciphertext and does not
          receive the fragment key.
        </p>
        <p>
          Paste reads stop working as soon as their server-enforced expiry is
          reached. A background cleanup process subsequently reclaims the
          expired database rows. View-once content is deleted by the first
          successful deliberate reveal.
        </p>
      </section>
      <section>
        <h2>Live room data</h2>
        <p>
          LiveBin rooms are collaborative plaintext rooms. The service
          transiently processes and stores their unencrypted text, tab details,
          and optional password verifier to provide the room until expiry. Live
          rooms are not protected by the paste encryption feature.
        </p>
        <p>
          While a room is active, the service also keeps participant display
          names, joined time, connection state, selected tab, and cursor or
          selection information in process memory to show collaboration. This
          presence data is not stored in SQLite and is cleared on expiry or a
          service restart.
        </p>
      </section>
      <section>
        <h2>Connection and abuse-prevention data</h2>
        <p>
          Requests pass through Cloudflare, which processes connection details
          such as IP addresses and traffic metadata under its{" "}
          <a href={cloudflarePrivacyURL}>privacy policy</a>. 0xbin temporarily
          uses a resolved network address in memory to enforce rate limits and
          detect repeated missing-link requests. Inactive limiter entries are
          removed after approximately two hours. The application does not
          currently place visitor IP addresses in its request logs or paste
          database.
        </p>
      </section>
      <section>
        <h2>Browser storage and analytics</h2>
        <p>
          0xbin stores your light or dark theme preference in local browser
          storage. For each LiveBin room, it also stores a random room-scoped
          participant credential and the last authoritative nickname so normal
          tabs, reloads, and reopenings represent one participant. Different
          rooms cannot use that credential to correlate participants. Clearing
          site data creates a new participant identity.
        </p>
        <p>
          LiveBin room access and creator authority use room-scoped HttpOnly
          cookies. The creator cookie is not a recoverable account: clearing it
          loses creator authority. 0xbin does not store paste bodies, live-room
          text, passwords, or encryption keys in persistent script-visible
          browser storage. The hosted interface currently uses no advertising,
          third-party analytics, tracking cookies, or third-party executable
          scripts.
        </p>
      </section>
      <section>
        <h2>Disclosure and legal requests</h2>
        <p>
          Information may be disclosed when reasonably necessary to operate and
          secure the service, investigate abuse, comply with applicable law, or
          respond to a valid legal request. Because pastes expire quickly,
          requested information may no longer be available.
        </p>
      </section>
      <section>
        <h2>Your choices and privacy contact</h2>
        <p>
          Do not submit personal or sensitive information unless you are
          authorized to share it. Use client-side encryption for sensitive
          content. For privacy questions or requests, email{" "}
          <a href={contactHref}>{contactEmail}</a> and include enough detail to
          identify the request without sending an encryption key.
        </p>
      </section>
      <section>
        <h2>How to report abuse</h2>
        <p>
          Email <a href={contactHref}>{contactEmail}</a> with:
        </p>
        <ul>
          <li>the complete 0xbin paste URL without an encryption key;</li>
          <li>the type of abuse or right affected;</li>
          <li>enough context to evaluate the report; and</li>
          <li>a reliable way to contact you if clarification is required.</li>
        </ul>
        <p>
          Do not send passwords, private keys, identity documents, or unrelated
          personal data. Do not attempt to access content you are not authorized
          to view.
        </p>
      </section>
      <section>
        <h2>What happens next</h2>
        <p>
          Reports are reviewed against the acceptable-use rules and applicable
          obligations. Content may already be unavailable because all pastes
          expire within three days. Encrypted content cannot be inspected
          without a key; do not send a fragment key unless it is necessary,
          lawful, and specifically requested.
        </p>
      </section>
      <section>
        <h2>Copyright and legal requests</h2>
        <p>
          Clearly identify the protected work or legal right, the reported URL,
          your authority to act, and the requested action. False or misleading
          reports may harm users and delay legitimate requests.
        </p>
      </section>
    </>
  );
}

function MenuIcon() {
  return (
    <svg viewBox="0 0 20 20" aria-hidden="true">
      <circle cx="4" cy="10" r="1" />
      <circle cx="10" cy="10" r="1" />
      <circle cx="16" cy="10" r="1" />
    </svg>
  );
}
