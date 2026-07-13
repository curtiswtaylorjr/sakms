// Registers @testing-library/jest-dom's matchers on Vitest's `expect`
// (e.g. toBeInTheDocument, toHaveTextContent) and pulls in the type
// augmentation for them. @solidjs/testing-library auto-cleans the DOM
// between tests when Vitest's globals (afterEach) are available, which they
// are (vitest.config.ts sets globals: true).
import "@testing-library/jest-dom/vitest";
