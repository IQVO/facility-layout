import type {ReactNode} from 'react';
import clsx from 'clsx';
import Link from '@docusaurus/Link';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import Layout from '@theme/Layout';
import HomepageFeatures from '@site/src/components/HomepageFeatures';
import Heading from '@theme/Heading';

import styles from './index.module.css';

const CODE_SEGMENTS: {label: string; value: string}[] = [
  {label: 'Site', value: 'WH1'},
  {label: 'Area', value: 'STOR'},
  {label: 'Zone', value: 'AMB'},
  {label: 'Aisle', value: 'A07'},
  {label: 'Bay', value: '03'},
  {label: 'Level', value: '02'},
  {label: 'Position', value: 'B'},
];

function StudyDisclaimer() {
  return (
    <div
      style={{
        background: '#fef3c7',
        color: '#78350f',
        textAlign: 'center',
        padding: '0.6rem 1rem',
        fontSize: '0.9rem',
        borderBottom: '1px solid #f59e0b',
      }}>
      ⚠️ <strong>Study project</strong> — an educational DDD exercise
      following real industry-standard patterns (the
      Site-Area-Zone-Aisle-Bay-Level-Position location-code hierarchy). Not
      a production system. Not affiliated with, endorsed by, or
      representative of Amazon or any other company.
    </div>
  );
}

function HomepageHeader() {
  const {siteConfig} = useDocusaurusContext();
  return (
    <header className={clsx('hero', styles.heroBanner)}>
      <StudyDisclaimer />
      <div className="container">
        <p className={styles.eyebrow}>
          warehouse-systems · Generic Subdomain
        </p>
        <Heading as="h1" className="hero__title">
          {siteConfig.title}
        </Heading>
        <p className={clsx('hero__subtitle', styles.subtitle)}>
          {siteConfig.tagline}
        </p>

        <div className={styles.codeStrip} aria-label="Example location code">
          {CODE_SEGMENTS.map((segment, i) => (
            <span key={segment.label} className={styles.segmentGroup}>
              <span className={styles.segment}>
                <span className={styles.segmentValue}>{segment.value}</span>
                <span className={styles.segmentLabel}>{segment.label}</span>
              </span>
              {i < CODE_SEGMENTS.length - 1 && (
                <span className={styles.hyphen}>-</span>
              )}
            </span>
          ))}
        </div>

        <div className={styles.buttons}>
          <Link
            className="button button--primary button--lg"
            to="/docs/overview">
            Read the documentation
          </Link>
          <Link
            className="button button--secondary button--lg"
            to="/docs/api-reference">
            API reference
          </Link>
          <Link
            className="button button--secondary button--lg"
            to="/docs/ecosystem/context-map">
            Context map
          </Link>
        </div>
      </div>
    </header>
  );
}

function WhatItOwns() {
  return (
    <section className={styles.ownership}>
      <div className="container">
        <div className="row">
          <div className="col col--6">
            <Heading as="h2" className={styles.ownsHeading}>
              What it owns
            </Heading>
            <ul className={styles.ownsList}>
              <li>Whether a coded location exists</li>
              <li>Whether it is Active, under maintenance, or retired</li>
              <li>Which kinds of storage are legal in which zones</li>
              <li>Aisle walk order and direction, for travel-path planning</li>
            </ul>
          </div>
          <div className="col col--6">
            <Heading as="h2" className={styles.ownsHeading}>
              What it refuses to own
            </Heading>
            <ul className={styles.ownsList}>
              <li>
                Stock, occupancy and reservations — that is{' '}
                <code>inventory-storage</code>
              </li>
              <li>
                SKU master data (handling classification, unit dimensions
                and weight) — that is <code>product-master</code>
              </li>
              <li>
                Tasks, assignments and dispatch — that is{' '}
                <code>fulfillment-execution</code>
              </li>
              <li>
                Work release and flow balancing — that is{' '}
                <code>wes-work-planning</code>
              </li>
              <li>Any write into another bounded context. Ever.</li>
            </ul>
          </div>
        </div>
      </div>
    </section>
  );
}

export default function Home(): ReactNode {
  const {siteConfig} = useDocusaurusContext();
  return (
    <Layout
      title={siteConfig.title}
      description="The warehouse map: Site, Zone, Aisle and coded LocationSlots, the placement rules that gate them, and the read models that draw them.">
      <HomepageHeader />
      <main>
        <HomepageFeatures />
        <WhatItOwns />
      </main>
    </Layout>
  );
}
