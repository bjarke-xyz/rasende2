// Code generated by templ - DO NOT EDIT.

// templ: version: v0.2.747
package components

//lint:file-ignore SA4006 This context is only used if a nested component is present.

import "github.com/a-h/templ"
import templruntime "github.com/a-h/templ/runtime"

type SearchViewModel struct {
	Base BaseViewModel
}

func search() templ.Component {
	return templruntime.GeneratedTemplate(func(templ_7745c5c3_Input templruntime.GeneratedComponentInput) (templ_7745c5c3_Err error) {
		templ_7745c5c3_W, ctx := templ_7745c5c3_Input.Writer, templ_7745c5c3_Input.Context
		templ_7745c5c3_Buffer, templ_7745c5c3_IsBuffer := templruntime.GetBuffer(templ_7745c5c3_W)
		if !templ_7745c5c3_IsBuffer {
			defer func() {
				templ_7745c5c3_BufErr := templruntime.ReleaseBuffer(templ_7745c5c3_Buffer)
				if templ_7745c5c3_Err == nil {
					templ_7745c5c3_Err = templ_7745c5c3_BufErr
				}
			}()
		}
		ctx = templ.InitializeContext(ctx)
		templ_7745c5c3_Var1 := templ.GetChildren(ctx)
		if templ_7745c5c3_Var1 == nil {
			templ_7745c5c3_Var1 = templ.NopComponent
		}
		ctx = templ.ClearChildren(ctx)
		_, templ_7745c5c3_Err = templ_7745c5c3_Buffer.WriteString("<div class=\"m-4\"><div class=\"flex justify-center\"><div class=\"flex flex-row align-middle space-x-2\"><form class=\"flex flex-row gap-4\"><input value=\"rasende\" type=\"search\" name=\"search\" hx-post=\"/search\" hx-trigger=\"change from:[name=&#39;content&#39;], load, input changed delay:300ms, search\" hx-target=\"#search-results\" hx-indicator=\".htmx-indicator\" hx-include=\"[name=&#39;content&#39;]\" class=\"p-1 block border border-solid border-gray-300 rounded-md dark: text-slate-900\">")
		if templ_7745c5c3_Err != nil {
			return templ_7745c5c3_Err
		}
		templ_7745c5c3_Err = barsSvg().Render(ctx, templ_7745c5c3_Buffer)
		if templ_7745c5c3_Err != nil {
			return templ_7745c5c3_Err
		}
		_, templ_7745c5c3_Err = templ_7745c5c3_Buffer.WriteString("<div><input name=\"include-charts\" type=\"hidden\" value=\"on\"> <input name=\"content\" type=\"checkbox\" class=\"cursor-pointer mr-1\" id=\"checkbox\"> <label class=\"inline-block\" for=\"checkbox\">Søg i artikel indhold</label></div></form></div></div><div class=\"flex flex-col justify-center mt-16\"><div id=\"search-results\"></div></div></div>")
		if templ_7745c5c3_Err != nil {
			return templ_7745c5c3_Err
		}
		return templ_7745c5c3_Err
	})
}

func Search(model SearchViewModel) templ.Component {
	return templruntime.GeneratedTemplate(func(templ_7745c5c3_Input templruntime.GeneratedComponentInput) (templ_7745c5c3_Err error) {
		templ_7745c5c3_W, ctx := templ_7745c5c3_Input.Writer, templ_7745c5c3_Input.Context
		templ_7745c5c3_Buffer, templ_7745c5c3_IsBuffer := templruntime.GetBuffer(templ_7745c5c3_W)
		if !templ_7745c5c3_IsBuffer {
			defer func() {
				templ_7745c5c3_BufErr := templruntime.ReleaseBuffer(templ_7745c5c3_Buffer)
				if templ_7745c5c3_Err == nil {
					templ_7745c5c3_Err = templ_7745c5c3_BufErr
				}
			}()
		}
		ctx = templ.InitializeContext(ctx)
		templ_7745c5c3_Var2 := templ.GetChildren(ctx)
		if templ_7745c5c3_Var2 == nil {
			templ_7745c5c3_Var2 = templ.NopComponent
		}
		ctx = templ.ClearChildren(ctx)
		templ_7745c5c3_Err = Layout(model.Base, search()).Render(ctx, templ_7745c5c3_Buffer)
		if templ_7745c5c3_Err != nil {
			return templ_7745c5c3_Err
		}
		return templ_7745c5c3_Err
	})
}

func barsSvg() templ.Component {
	return templruntime.GeneratedTemplate(func(templ_7745c5c3_Input templruntime.GeneratedComponentInput) (templ_7745c5c3_Err error) {
		templ_7745c5c3_W, ctx := templ_7745c5c3_Input.Writer, templ_7745c5c3_Input.Context
		templ_7745c5c3_Buffer, templ_7745c5c3_IsBuffer := templruntime.GetBuffer(templ_7745c5c3_W)
		if !templ_7745c5c3_IsBuffer {
			defer func() {
				templ_7745c5c3_BufErr := templruntime.ReleaseBuffer(templ_7745c5c3_Buffer)
				if templ_7745c5c3_Err == nil {
					templ_7745c5c3_Err = templ_7745c5c3_BufErr
				}
			}()
		}
		ctx = templ.InitializeContext(ctx)
		templ_7745c5c3_Var3 := templ.GetChildren(ctx)
		if templ_7745c5c3_Var3 == nil {
			templ_7745c5c3_Var3 = templ.NopComponent
		}
		ctx = templ.ClearChildren(ctx)
		_, templ_7745c5c3_Err = templ_7745c5c3_Buffer.WriteString("<svg width=\"32\" height=\"32\" viewBox=\"0 0 135 140\" xmlns=\"http://www.w3.org/2000/svg\" class=\"htmx-indicator fill-slate-900 dark:fill-slate-50\"><rect y=\"10\" width=\"15\" height=\"120\" rx=\"6\"><animate attributeName=\"height\" begin=\"0.5s\" dur=\"1s\" values=\"120;110;100;90;80;70;60;50;40;140;120\" calcMode=\"linear\" repeatCount=\"indefinite\"></animate> <animate attributeName=\"y\" begin=\"0.5s\" dur=\"1s\" values=\"10;15;20;25;30;35;40;45;50;0;10\" calcMode=\"linear\" repeatCount=\"indefinite\"></animate></rect> <rect x=\"30\" y=\"10\" width=\"15\" height=\"120\" rx=\"6\"><animate attributeName=\"height\" begin=\"0.25s\" dur=\"1s\" values=\"120;110;100;90;80;70;60;50;40;140;120\" calcMode=\"linear\" repeatCount=\"indefinite\"></animate> <animate attributeName=\"y\" begin=\"0.25s\" dur=\"1s\" values=\"10;15;20;25;30;35;40;45;50;0;10\" calcMode=\"linear\" repeatCount=\"indefinite\"></animate></rect> <rect x=\"60\" width=\"15\" height=\"140\" rx=\"6\"><animate attributeName=\"height\" begin=\"0s\" dur=\"1s\" values=\"120;110;100;90;80;70;60;50;40;140;120\" calcMode=\"linear\" repeatCount=\"indefinite\"></animate> <animate attributeName=\"y\" begin=\"0s\" dur=\"1s\" values=\"10;15;20;25;30;35;40;45;50;0;10\" calcMode=\"linear\" repeatCount=\"indefinite\"></animate></rect> <rect x=\"90\" y=\"10\" width=\"15\" height=\"120\" rx=\"6\"><animate attributeName=\"height\" begin=\"0.25s\" dur=\"1s\" values=\"120;110;100;90;80;70;60;50;40;140;120\" calcMode=\"linear\" repeatCount=\"indefinite\"></animate> <animate attributeName=\"y\" begin=\"0.25s\" dur=\"1s\" values=\"10;15;20;25;30;35;40;45;50;0;10\" calcMode=\"linear\" repeatCount=\"indefinite\"></animate></rect> <rect x=\"120\" y=\"10\" width=\"15\" height=\"120\" rx=\"6\"><animate attributeName=\"height\" begin=\"0.5s\" dur=\"1s\" values=\"120;110;100;90;80;70;60;50;40;140;120\" calcMode=\"linear\" repeatCount=\"indefinite\"></animate> <animate attributeName=\"y\" begin=\"0.5s\" dur=\"1s\" values=\"10;15;20;25;30;35;40;45;50;0;10\" calcMode=\"linear\" repeatCount=\"indefinite\"></animate></rect></svg>")
		if templ_7745c5c3_Err != nil {
			return templ_7745c5c3_Err
		}
		return templ_7745c5c3_Err
	})
}
