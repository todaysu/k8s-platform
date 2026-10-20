import { expect, it } from 'vitest'
import { parseKubectlCommand } from './kubectlCommand'
it('parses kubectl arguments including JSON patches without a shell',()=>{expect(parseKubectlCommand(`kubectl patch deployment web -n app -p '{"spec":{"replicas":2}}'`)).toEqual(['patch','deployment','web','-n','app','-p','{"spec":{"replicas":2}}']);expect(parseKubectlCommand('get pods -A')).toEqual(['get','pods','-A'])})
it('rejects pipes, substitutions and incomplete quotes',()=>{for(const command of ['kubectl get pods | cat','kubectl get pods; ls','kubectl get $(cat x)',"kubectl get 'pods"]){expect(()=>parseKubectlCommand(command)).toThrow()}})
