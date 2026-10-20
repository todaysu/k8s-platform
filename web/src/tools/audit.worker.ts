import {parseAudit} from './audit'
self.onmessage=(event:MessageEvent<string>)=>{try{self.postMessage({data:parseAudit(event.data)})}catch{self.postMessage({error:'日志解析失败，请检查文件格式。'})}}
