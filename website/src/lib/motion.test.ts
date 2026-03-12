import { createRevealMotion } from "./motion";

describe("createRevealMotion", () => {
  it("returns static motion props when reduced motion is enabled", () => {
    expect(createRevealMotion(true)).toEqual({
      initial: { opacity: 1, y: 0 },
      animate: { opacity: 1, y: 0 },
      transition: { duration: 0 },
    });
  });

  it("returns animated motion props when reduced motion is disabled", () => {
    expect(createRevealMotion(false, 0.2)).toMatchObject({
      initial: { opacity: 0, y: 18 },
      animate: { opacity: 1, y: 0 },
      transition: { duration: 0.5, delay: 0.2 },
    });
  });
});
