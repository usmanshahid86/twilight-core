import React from "react";
import Link from "@docusaurus/Link";
import Layout from "@theme/Layout";
import styles from "./index.module.css";

type Card = { title: string; desc: string; to: string; tag: string };

const cards: Card[] = [
  {
    tag: "Operators",
    title: "Run a node",
    desc: "Build, initialize, and run twilightd; spin up a localnet and a rewards smoke.",
    to: "/getting-started/quickstart",
  },
  {
    tag: "Validators",
    title: "CoreSlot validators",
    desc: "The PoA validator authority: slots, operator and payout addresses, reward weight.",
    to: "/operators/coreslot-operator-guide",
  },
  {
    tag: "Users",
    title: "Claim rewards",
    desc: "How rewards are minted and distributed, and how anyone can claim them.",
    to: "/rewards/claims",
  },
  {
    tag: "Concepts",
    title: "Rewards economics",
    desc: "Supply-threshold halving, epoch finalization, and uniform active-block distribution.",
    to: "/rewards/economics",
  },
  {
    tag: "Developers",
    title: "Build & contribute",
    desc: "Repo map, module map, testing layers, and contribution conventions.",
    to: "/development/repo-map",
  },
  {
    tag: "Auditors",
    title: "Validation & safety",
    desc: "What each phase proved, the five invariants, and fail-closed behavior.",
    to: "/reference/validation-reports",
  },
];

export default function Home(): React.ReactElement {
  return (
    <Layout
      title="Twilight Chain Docs"
      description="Documentation for the Twilight CoreSlot Proof-of-Authority chain and the utwlt rewards module."
    >
      <header className={styles.hero}>
        <div className={styles.heroInner}>
          <span className={styles.eyebrow}>CoreSlot PoA · utwlt rewards</span>
          <h1 className={styles.title}>Twilight Chain</h1>
          <p className={styles.tagline}>
            A minimal Cosmos SDK Proof-of-Authority chain with scheduled{" "}
            <code>utwlt</code> block rewards — finalized per epoch and claimed to
            CoreSlot operators.
          </p>
          <div className={styles.ctaRow}>
            <Link className={styles.ctaPrimary} to="/getting-started/overview">
              Get started
            </Link>
            <Link className={styles.ctaSecondary} to="/rewards/overview">
              Rewards overview →
            </Link>
          </div>
          <p className={styles.statusNote}>
            Validated through Phase 10. Production zero-premine genesis and longer
            soak drills remain Phase 11 — not yet proven, not mainnet-ready.
          </p>
        </div>
      </header>

      <main className={styles.main}>
        <div className={styles.cards}>
          {cards.map((c) => (
            <Link key={c.title} className={styles.card} to={c.to}>
              <span className={styles.cardTag}>{c.tag}</span>
              <h3 className={styles.cardTitle}>{c.title}</h3>
              <p className={styles.cardDesc}>{c.desc}</p>
              <span className={styles.cardArrow}>→</span>
            </Link>
          ))}
        </div>
      </main>
    </Layout>
  );
}
