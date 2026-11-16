// @ts-check
/** @param {Element} row */
function configureRow(row) {
 const kind=row.querySelector('[data-kind]');
 if (!(kind instanceof HTMLSelectElement)) throw new Error('Missing source type');
 for(const group of row.querySelectorAll('[data-for]')) {
  if (!(group instanceof HTMLElement)) continue;
  const visible=group.dataset.for===kind.value || group.dataset.for==='remote' && kind.value!=='file';
  group.hidden=!visible;
  for(const field of group.querySelectorAll('input')) {
   if (!(field instanceof HTMLInputElement)) continue;
   field.disabled=!visible;
   if(field.type==='url') field.required=visible && field.dataset.required==='yes';
   if(field.type==='file') field.required=visible && !(row.getAttribute('data-cached')==='yes' && row.getAttribute('data-cached-kind')===kind.value);
   if(field.type==='number' && visible && Number(field.value)<1)field.value='24';
  }
 }
}
const sourceList=document.getElementById('sources');
const addSource=document.getElementById('add-source');
const sourceTemplate=document.getElementById('source-template');
if(sourceList instanceof HTMLElement && addSource instanceof HTMLButtonElement && sourceTemplate instanceof HTMLTemplateElement) {
 /** @param {Element} row */
 const attach=row=>{
  configureRow(row);
  row.querySelector('[data-kind]')?.addEventListener('change',()=>configureRow(row));
  row.querySelector('[data-remove]')?.addEventListener('click',()=>{row.remove();addSource.disabled=sourceList.children.length>=8;});
 };
 for(const row of sourceList.children) attach(row);
 addSource.disabled=sourceList.children.length>=8;
 addSource.addEventListener('click',()=>{
  if(sourceList.children.length>=8)return;
  const used=new Set(Array.from(sourceList.children,row=>row.getAttribute('data-index')));
  let index=0;while(used.has(String(index)) && index<8)index++;
  const fragment=sourceTemplate.content.cloneNode(true);
  if(!(fragment instanceof DocumentFragment))throw new Error('Invalid template');
  for(const element of fragment.querySelectorAll('*'))for(const attr of ['name','id','for','data-index']){
   const value=element.getAttribute(attr);if(value!==null)element.setAttribute(attr,value.replace('__index__',String(index)));
  }
  const row=fragment.firstElementChild;if(row===null)throw new Error('Missing source row');
  sourceList.appendChild(fragment);attach(row);addSource.disabled=sourceList.children.length>=8;
 });
}
for(const time of document.querySelectorAll('time')) {
 if(!(time instanceof HTMLTimeElement))continue;
 const date=new Date(time.dateTime);if(!Number.isNaN(date.getTime()))time.textContent=date.toLocaleString(document.documentElement.lang);
}
