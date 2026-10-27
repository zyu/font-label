import { describe, expect, it } from "vitest";
import { exceptionReasons, stringSetting } from "../settings";

describe("配置数组容错", () => {
  it.each([undefined, null, { value: [] }, { a: 1 }, 1, true, "yes"])("%j 回退为空数组", (value) => {
    expect(stringSetting(value)).toEqual([]);
    expect(exceptionReasons(value)).toEqual([]);
  });
  it("保留有效字符串，过滤不合法的数组元素", () => {
    expect(stringSetting([null, 1, {}, [], " ", "有效评语"])).toEqual(["有效评语"]);
  });
  it("保留有效原因，过滤空字段、错误类型和重复 key", () => {
    expect(exceptionReasons([null, 1, "blur", {}, [], { key: "blur" }, { key: "", label: "空" }, { key: "blank", label: " " }, { key: "blur", label: "模糊" }, { key: "blur", label: "重复" }, { key: " blur", label: "有空格" }])).toEqual([{ key: "blur", label: "模糊" }]);
  });
});
