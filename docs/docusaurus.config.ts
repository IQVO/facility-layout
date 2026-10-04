import {themes as prismThemes} from 'prism-react-renderer';
import type {Config} from '@docusaurus/types';
import type * as Preset from '@docusaurus/preset-classic';
import type * as OpenApiPlugin from 'docusaurus-plugin-openapi-docs';

const config: Config = {
  title: 'Facility Layout',
  tagline:
    'The system of record for where things physically are in the building — the warehouse map other contexts read but never write.',
  favicon: 'img/favicon.ico',

  future: {
    v4: true,
  },

  url: 'https://iqvo.github.io',
  baseUrl: '/facility-layout/',

  organizationName: 'IQVO',
  projectName: 'facility-layout',
  deploymentBranch: 'gh-pages',
  trailingSlash: false,

  onBrokenLinks: 'throw',
  onBrokenAnchors: 'throw',

  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },

  markdown: {
    mermaid: true,
    hooks: {
      onBrokenMarkdownLinks: 'throw',
      onBrokenMarkdownImages: 'throw',
    },
  },

  presets: [
    [
      'classic',
      {
        docs: {
          sidebarPath: './sidebars.ts',
          editUrl:
            'https://github.com/IQVO/facility-layout/tree/main/docs/',
          docItemComponent: '@theme/ApiItem',
        },
        blog: false,
        theme: {
          customCss: './src/css/custom.css',
        },
      } satisfies Preset.Options,
    ],
  ],

  plugins: [
    'docusaurus-plugin-sass',
    [
      'docusaurus-plugin-openapi-docs',
      {
        id: 'openapi',
        docsPluginId: 'classic',
        config: {
          facility: {
            specPath: '../apis/openapi.yaml',
            outputDir: 'docs/api-reference/rest',
            downloadUrl:
              'https://raw.githubusercontent.com/IQVO/facility-layout/main/apis/openapi.yaml',
            sidebarOptions: {
              groupPathsBy: 'tag',
              categoryLinkSource: 'tag',
            },
          } satisfies OpenApiPlugin.Options,
        },
      },
    ],
  ],

  themes: ['@docusaurus/theme-mermaid', 'docusaurus-theme-openapi-docs'],

  themeConfig: {
    image: 'img/logo.svg',
    colorMode: {
      respectPrefersColorScheme: true,
    },
    navbar: {
      title: 'Facility Layout',
      logo: {
        alt: 'Facility Layout',
        src: 'img/logo.svg',
      },
      items: [
        {
          type: 'docSidebar',
          sidebarId: 'docsSidebar',
          position: 'left',
          label: 'Documentation',
        },
        {
          to: '/docs/api-reference/rest/facility-layout-api',
          label: 'API',
          position: 'left',
        },
        {
          to: '/docs/adr',
          label: 'ADR',
          position: 'left',
        },
        {
          href: 'https://github.com/IQVO/facility-layout',
          label: 'GitHub',
          position: 'right',
        },
      ],
    },
    footer: {
      style: 'dark',
      links: [
        {
          title: 'Documentation',
          items: [
            {label: 'Overview', to: '/docs/overview'},
            {label: 'Business Context', to: '/docs/business-context/domain-vision'},
            {label: 'Domain-Driven Design', to: '/docs/ddd/subdomain-classification'},
            {label: 'API Reference', to: '/docs/api-reference'},
          ],
        },
        {
          title: 'Ecosystem',
          items: [
            {label: 'Context map', to: '/docs/ecosystem/context-map'},
            {
              label: 'inventory-storage',
              href: 'https://github.com/IQVO/inventory-storage',
            },
            {
              label: 'wes-work-planning',
              href: 'https://github.com/IQVO/wes-work-planning',
            },
            {
              label: 'fulfillment-execution',
              href: 'https://github.com/IQVO/fulfillment-execution',
            },
            {
              label: 'workforce-management',
              href: 'https://github.com/IQVO/workforce-management',
            },
          ],
        },
        {
          title: 'Source',
          items: [
            {
              label: 'facility-layout on GitHub',
              href: 'https://github.com/IQVO/facility-layout',
            },
            {
              label: 'OpenAPI specification',
              href: 'https://github.com/IQVO/facility-layout/blob/main/apis/openapi.yaml',
            },
          ],
        },
      ],
      copyright: `warehouse-systems · facility-layout — Generic Subdomain. Built ${new Date().getFullYear()}.`,
    },
    prism: {
      theme: prismThemes.github,
      darkTheme: prismThemes.dracula,
      additionalLanguages: ['bash', 'json', 'go', 'sql', 'yaml'],
    },
  } satisfies Preset.ThemeConfig,
};

export default config;
