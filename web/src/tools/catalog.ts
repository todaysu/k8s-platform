// Add tools here; every tool owns a route and reuses the same catalog layout.
export const operationsTools=[{
 id:'audit',title:'审计日志分析',description:'导入 audit 日志，追踪资源的发布操作、操作者及失败请求。',tag:'本地解析 · 无需安装组件',path:'/tools/audit',symbol:'≡',
},{
 id:'jvm',title:'JVM 内存分析',description:'导入 jstat、GC.heap_info、类直方图和 GC 日志，判断老年代增长、Full GC、对象滞留与堆外内存风险。',tag:'本地解析 · 无需安装组件',path:'/tools/jvm',symbol:'J',
}] as const
