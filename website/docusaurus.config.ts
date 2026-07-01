import type { Config } from "@docusaurus/types";
import type * as Preset from "@docusaurus/preset-classic";
import { themes as prismThemes } from "prism-react-renderer";

// Twilight Chain documentation site.
// Fresh, self-contained Docusaurus project. All content is authored from the
// twilight-core repository (code + phase reports). Visual language only evokes the
// Twilight brand; nothing is copied from other Twilight sites.
const config: Config = {
  title: "Twilight Chain Docs",
  tagline: "A CoreSlot Proof-of-Authority chain with utwlt rewards",

  // Pages target: project site under the current owner. After the repo moves to
  // the twilight-project org, retarget it in FOUR places: url ->
  // https://twilight-project.github.io, organizationName -> "twilight-project",
  // and the two github.com/usmanshahid86/twilight-core links below (the docs
  // `editUrl` and the navbar `href`). baseUrl stays /twilight-core/.
  url: "https://usmanshahid86.github.io",
  baseUrl: "/twilight-core/",
  organizationName: "usmanshahid86",
  projectName: "twilight-core",
  favicon: "img/twilight.svg",

  // Fail the build on any dead internal link or anchor.
  onBrokenLinks: "throw",
  onBrokenAnchors: "throw",

  i18n: { defaultLocale: "en", locales: ["en"] },

  markdown: {
    mermaid: true,
    hooks: { onBrokenMarkdownLinks: "throw" },
  },
  themes: ["@docusaurus/theme-mermaid"],

  // Brand fonts (Google Fonts for now; self-hosting tracked as later hardening).
  headTags: [
    {
      tagName: "link",
      attributes: { rel: "preconnect", href: "https://fonts.googleapis.com" },
    },
    {
      tagName: "link",
      attributes: {
        rel: "preconnect",
        href: "https://fonts.gstatic.com",
        crossorigin: "anonymous",
      },
    },
    {
      tagName: "link",
      attributes: {
        rel: "stylesheet",
        href: "https://fonts.googleapis.com/css2?family=Inter:wght@300;400;500;600;700&family=Instrument+Serif:ital@0;1&family=Roboto+Mono:wght@400;500&display=swap",
      },
    },
  ],

  presets: [
    [
      "classic",
      {
        docs: {
          routeBasePath: "/",
          sidebarPath: "./sidebars.ts",
          editUrl:
            "https://github.com/usmanshahid86/twilight-core/tree/main/website/",
        },
        blog: false,
        theme: { customCss: "./src/css/custom.css" },
      } satisfies Preset.Options,
    ],
  ],

  themeConfig: {
    colorMode: { defaultMode: "dark", respectPrefersColorScheme: false },
    image: "img/twilight.png",
    navbar: {
      title: "Twilight",
      logo: {
        alt: "Twilight",
        src: "img/twilight.svg",
        srcDark: "img/twilight.png",
      },
      items: [
        { type: "docSidebar", sidebarId: "docs", position: "left", label: "Docs" },
        {
          href: "https://github.com/usmanshahid86/twilight-core",
          label: "GitHub",
          position: "right",
        },
      ],
    },
    footer: {
      style: "dark",
      links: [],
      copyright: "Twilight Chain Docs — built from the twilight-core repository.",
    },
    prism: {
      theme: prismThemes.dracula,
      darkTheme: prismThemes.dracula,
      additionalLanguages: ["bash", "json", "go", "toml"],
    },
  } satisfies Preset.ThemeConfig,
};

export default config;
