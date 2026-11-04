/**
 * 页面级排版权元的单一入口。
 *
 * 这一层只做转出，不引入任何 hook，因此服务端组件可以直接引用
 * （components/editorial*.tsx 刻意保持 hook-free 就是为了这一点）。
 *
 * 之所以留这一层、而不是让 63 个页面直接 import components/editorial*：
 * 全部页面共用一个入口，之后要调整某个原语的版式，只需在这里换一处，
 * 不必再动 63 个文件的 import。这是后续「内容重构」的落点。
 */

export type { PanelAnnotationKind, SeverityBarItem } from "@/components/editorial";
export {
  ApiBar,
  ComposeBar,
  KpiBand,
  KpiRow,
  Masthead,
  Panel,
  PanelGrid,
  SectionHead,
  SeverityBar,
} from "@/components/editorial";
export {
  GroupHead,
  RecordRow,
  RecordSub,
  Sheet,
  SheetFoot,
  SheetHead,
} from "@/components/editorial-sheet";
