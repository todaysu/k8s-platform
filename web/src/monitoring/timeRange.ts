export type TimeRange={start:string;end:string}
export function validateTimeRange(range:TimeRange,now=Date.now()){
 const start=Date.parse(range.start),end=Date.parse(range.end)
 if(!Number.isFinite(start)||!Number.isFinite(end))return '请输入完整的开始和结束时间。'
 if(end<=start)return '结束时间必须晚于开始时间。'
 if(end-start<30000)return '时间范围至少为 30 秒。'
 if(end-start>7*24*3600000)return '单次查询最多支持 7 天，请缩小时间范围。'
 if(end>now+60000)return '结束时间不能晚于当前时间。'
 return ''
}
export const localDateTime=(value:string)=>{const date=new Date(value);if(!Number.isFinite(date.getTime()))return '';return new Date(date.getTime()-date.getTimezoneOffset()*60000).toISOString().slice(0,19)}
