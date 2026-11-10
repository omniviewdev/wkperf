export const PodRow = (props) => {
  return props.items.map((item) => item.name).join(",");
};
export function Table({ rows }) {
  return rows.map((r) => PodRow({ items: r })).length;
}
console.log(Table({ rows: [[{ name: "a" }]] }));
